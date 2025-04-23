package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coyove/common/sched"
	"github.com/coyove/goflyway"
	"github.com/coyove/goflyway/v"
	"golang.org/x/crypto/acme/autocert"
)

var (
	version      = "__devel__"
	remoteAddr   string
	localAddr    string
	addr         string
	httpsProxy   string
	resetTraffic bool
	cconfig      = &goflyway.ClientConfig{}
	sconfig      = &goflyway.ServerConfig{}
)

func printHelp(a ...interface{}) {
	if len(a) > 0 {
		fmt.Printf("goflyway: ")
		fmt.Println(a...)
	}
	fmt.Println("usage: goflyway -DLhHUvkqpPtTwWy address:port")
	flag.PrintDefaults()
	os.Exit(0)
}

func runServer(addr string, sconfig *goflyway.ServerConfig) error {
	if httpsProxy != "" {
		v.Vprint("server listen on ", addr, " (https://", httpsProxy, ")")
		m := &autocert.Manager{
			Cache:      autocert.DirCache("secret-dir"),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(httpsProxy),
		}
		s := &http.Server{
			Addr:      addr,
			TLSConfig: m.TLSConfig(),
		}
		for i, p := range s.TLSConfig.NextProtos {
			if p == "h2" {
				s.TLSConfig.NextProtos[i] = "h2-disabled"
			}
		}
		s.Handler = &connector{
			timeout: sconfig.Timeout,
			auth:    sconfig.Key,
		}
		if os.Getenv("GFW_TEST") == "1" {
			return s.ListenAndServe()
		}
		return s.ListenAndServeTLS("", "")
	}

	v.Vprint("server listen on ", addr)
	return goflyway.NewServer(addr, sconfig)
}

func runClient(localAddr, remoteAddr, addr string, cconfig *goflyway.ClientConfig, resetTraffic bool) error {
	cconfig.Bind = remoteAddr
	cconfig.Upstream = addr
	cconfig.Stat = &goflyway.Traffic{}

	if v.Verbose > 0 {
		go watchTraffic(cconfig, resetTraffic)
	}
	if cconfig.Dynamic {
		v.Vprint("dynamic: forward ", localAddr, " to * through ", addr)
	} else {
		v.Vprint("forward ", localAddr, " to ", remoteAddr, " through ", addr)
	}
	if cconfig.WebSocket {
		v.Vprint("relay: use Websocket protocol")
	}
	if a := os.Getenv("http_proxy") + os.Getenv("HTTP_PROXY"); a != "" {
		v.Vprint("note: system HTTP proxy is set to: ", a)
	}
	if a := os.Getenv("https_proxy") + os.Getenv("HTTPS_PROXY"); a != "" {
		v.Vprint("note: system HTTPS proxy is set to: ", a)
	}

	return goflyway.NewClient(localAddr, cconfig)
}

func main() {
	sched.Verbose = false

	// 定义标志
	var (
		showHelp       = flag.Bool("h", false, "Show help")
		isQuiet        = flag.Bool("q", false, "Quiet mode")
		isDynamic      = flag.Bool("D", false, "Enable dynamic mode")
		useWebSocket   = flag.Bool("w", false, "Use WebSocket protocol")
		resetStats     = flag.Bool("y", false, "Reset traffic statistics")
		verboseLevel   = flag.Int("v", 0, "Verbose level (1-3)")
		key            = flag.String("k", "", "Encryption key")
		altKey         = flag.String("p", "", "Alias for -k")
		timeout        = flag.String("t", "", "Timeout in seconds")
		speedLimit     = flag.String("T", "", "Speed limit in bytes per second")
		writeBuffer    = flag.String("W", "", "Write buffer size")
		proxyPass      = flag.String("P", "", "Proxy pass address")
		pathPattern    = flag.String("U", "", "URL path pattern")
		httpsProxyFlag = flag.String("H", "", "HTTPS proxy name")
		configFile     = flag.String("c", "", "Config file path")
		localAddrFlag  = flag.String("L", "", "Local address (format: [local_ip:]local_port[:remote_ip:]remote_port)")
	)

	// 自定义帮助函数
	flag.Usage = func() {
		printHelp()
	}

	// 解析
	flag.Parse()

	// 处理帮助
	if *showHelp {
		printHelp()
	}

	// 设置详细程度
	if *isQuiet {
		v.Verbose = -1
	} else {
		v.Verbose = *verboseLevel
	}

	// 设置配置
	cconfig.WebSocket = *useWebSocket
	cconfig.Dynamic = *isDynamic
	resetTraffic = *resetStats

	// 处理本地地址
	if *localAddrFlag != "" {
		parts := strings.Split(*localAddrFlag, ":")
		switch len(parts) {
		case 1:
			localAddr = ":" + parts[0]
		case 2:
			localAddr = *localAddrFlag
		case 3:
			localAddr, remoteAddr = ":"+parts[0], parts[1]+":"+parts[2]
		case 4:
			localAddr, remoteAddr = parts[0]+":"+parts[1], parts[2]+":"+parts[3]
		default:
			printHelp("illegal option -L", *localAddrFlag)
		}
	}

	// 处理其他参数
	if *key != "" {
		sconfig.Key, cconfig.Key = *key, *key
	}
	if *altKey != "" {
		sconfig.Key, cconfig.Key = *altKey, *altKey
	}
	if *timeout != "" {
		t, _ := strconv.ParseInt(*timeout+"000000000", 10, 64)
		*(*int64)(&cconfig.Timeout) = t
		sconfig.Timeout = cconfig.Timeout
	}
	if *speedLimit != "" {
		speed, _ := strconv.ParseInt(*speedLimit, 10, 64)
		sconfig.SpeedThrot = goflyway.NewTokenBucket(speed, speed*25)
	}
	if *writeBuffer != "" {
		writebuffer, _ := strconv.ParseInt(*writeBuffer, 10, 64)
		sconfig.WriteBuffer, cconfig.WriteBuffer = writebuffer, writebuffer
	}
	if *proxyPass != "" {
		sconfig.ProxyPassAddr = *proxyPass
	}
	if *pathPattern != "" {
		cconfig.PathPattern = *pathPattern
	}
	if *httpsProxyFlag != "" {
		cconfig.URLHeader = *httpsProxyFlag
		httpsProxy = *httpsProxyFlag
	}

	// 处理配置文件
	if *configFile != "" {
		buf, err := ioutil.ReadFile(*configFile)
		if err != nil {
			printHelp("failed to read config file:", err)
		}
		cmds := make(map[string]interface{})
		if err := json.Unmarshal(buf, &cmds); err != nil {
			printHelp("failed to parse config file:", err)
		}
		cconfig.Key, cconfig.VPN = cmds["password"].(string), true
		addr = fmt.Sprintf("%v:%v", cmds["server"], cmds["server_port"])
		v.Verbose = 3
		v.Vprint(os.Args, " config: ", cmds)
	}

	// 处理地址参数
	args := flag.Args()
	if len(args) > 0 {
		addr = args[0]
	}

	// 默认地址逻辑
	if addr == "" {
		if localAddr == "" {
			v.Vprint("assume you want a default server at :8100")
			addr = ":8100"
		} else {
			printHelp("missing address:port to listen/connect")
		}
	}

	// 处理远程地址
	if localAddr != "" && remoteAddr == "" {
		_, port, err1 := net.SplitHostPort(localAddr)
		host, _, err2 := net.SplitHostPort(addr)
		remoteAddr = host + ":" + port
		if err1 != nil || err2 != nil {
			printHelp("invalid address --", localAddr, addr)
		}
	}

	// 启动客户端或服务器
	if localAddr != "" && remoteAddr != "" {
		v.Eprint(runClient(localAddr, remoteAddr, addr, cconfig, resetTraffic))
	} else {
		v.Eprint(runServer(addr, sconfig))
	}
}

func watchTraffic(cconfig *goflyway.ClientConfig, reset bool) {
	path := filepath.Join(os.TempDir(), "goflyway_traffic")

	tmpbuf, _ := ioutil.ReadFile(path)
	if len(tmpbuf) != 16 || reset {
		tmpbuf = make([]byte, 16)
	}

	cconfig.Stat.Set(int64(binary.BigEndian.Uint64(tmpbuf)), int64(binary.BigEndian.Uint64(tmpbuf[8:])))

	var lastSent, lastRecv int64
	for range time.Tick(time.Second * 5) {
		s, r := *cconfig.Stat.Sent(), *cconfig.Stat.Recv()
		sv, rv := float64(s-lastSent)/1024/1024/5, float64(r-lastRecv)/1024/1024/5
		lastSent, lastRecv = s, r

		if sv >= 0.001 || rv >= 0.001 {
			v.Vprint("client send: ", float64(s)/1024/1024, "M (", sv, "M/s), recv: ", float64(r)/1024/1024, "M (", rv, "M/s)")
		}

		binary.BigEndian.PutUint64(tmpbuf, uint64(s))
		binary.BigEndian.PutUint64(tmpbuf[8:], uint64(r))
		ioutil.WriteFile(path, tmpbuf, 0644)
	}
}

type connector struct {
	book    TTLMap
	mu      sync.Mutex
	timeout time.Duration
	auth    string
}

func (c *connector) getClientIP(r *http.Request) string {
	clientIP := r.Header.Get("X-Forwarded-For")
	clientIP = strings.TrimSpace(strings.Split(clientIP, ",")[0])
	if clientIP == "" {
		clientIP = strings.TrimSpace(r.Header.Get("X-Real-Ip"))
	}
	if clientIP != "" {
		return clientIP
	}
	if ip, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return ip
	}
	return ""
}

func (c *connector) getFingerprint(r *http.Request) string {
	return r.UserAgent() //+ "/" + r.Header.Get("Accept-Language")
}

func (c *connector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	plain, iscurl := false, false
	pp := func() {
		path := r.URL.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		req, _ := http.NewRequest("GET", "https://arxiv.org"+path, nil)
		req.Header.Add("Accept", "text/html")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header().Add(k, v[0])
		}
		io.Copy(w, resp.Body)
	}

	if r.Method != "CONNECT" {
		if r.URL.Host == "" {
			pp()
			return
		}

		v.VVprint("plain http proxy: ", r.URL.Host)
		plain = true
	}

	// we are inside GFW and should pass data to upstream
	host := r.URL.Host
	if !regexp.MustCompile(`:\d+$`).MatchString(host) {
		if plain {
			host += ":80"
		} else {
			host += ":443"
		}
	}

	ip := c.getClientIP(r)

	if plain && host == c.auth+".com:80" {
		w.Header().Add("Content-Type", "text/html")
		w.Write([]byte(fmt.Sprintf("<title>Bookkeeper</title>Whitelisted: %s@%s", c.getFingerprint(r), c.getClientIP(r))))

		c.book.Add(ip, "white", 0)
		c.book.Add(c.getFingerprint(r), ip, 0)
		return
	}

	{ // Auth
		c.mu.Lock()
		state, _ := c.book.Get(ip)
		if state == "white" {
			goto OK
		} else if state == "black" {
			v.VVprint(ip, " is not known and is blocked")
		} else {
			authData := strings.TrimPrefix(r.Header.Get("Proxy-Authorization"), "Basic ")
			if authData == "" {
				authData = strings.TrimPrefix(r.Header.Get("Authentication"), "Basic ")
			}
			pa, err := base64.StdEncoding.DecodeString(strings.TrimSpace(authData))
			if err == nil && bytes.ContainsRune(pa, ':') {
				if string(pa[bytes.IndexByte(pa, ':')+1:]) == c.auth {
					goto OK
				}
			}

			ip2, ok := c.book.Get(c.getFingerprint(r))
			if !ok {
				c.mu.Unlock()
				pp()
				return
			}

			if state2, ok := c.book.Get(ip2); ok && state2 == "white" {
				v.VVprint(ip, " is not known, but fingerprint is okay")
				c.book.Delete(ip2)
				goto OK
			}
			v.VVprint(ip, " is not known nor is the fingerprint")
		}

		c.book.Add(ip, "black", time.Second*10)
		c.mu.Unlock()

		pp()
		return

	OK:
		c.book.Add(ip, "white", 0)
		c.book.Add(c.getFingerprint(r), ip, 0)
		c.mu.Unlock()

		iscurl = strings.Contains(strings.ToLower(r.UserAgent()), "curl/")
	}

	if plain {
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			w.WriteHeader(500)
			return
		}

		defer resp.Body.Close()

		for k := range w.Header() {
			w.Header().Del(k)
		}

		for k, v := range resp.Header {
			for _, v := range v {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		io.Copy(w, resp.Body)
		return
	}

	up, err := net.DialTimeout("tcp", host, c.timeout)
	if err != nil {
		v.Eprint(host, err)
		w.WriteHeader(500)
		return
	}

	hij, _ := w.(http.Hijacker) // No HTTP2
	proxyClient, _, err := hij.Hijack()
	if err != nil {
		v.Eprint(host, err)
		w.WriteHeader(500)
		return
	}

	if iscurl {
		proxyClient.Write([]byte("HTTP/1.1 200 OK\r\n"))
	} else {
		proxyClient.Write([]byte("HTTP/1.0 200 Connection Established\r\n"))
	}
	proxyClient.Write([]byte("Filler: "))
	for i := 0; i < rand.Intn(100)+300; i++ {
		proxyClient.Write([]byte("aaaaaaaa"))
	}
	proxyClient.Write([]byte("\r\n\r\n"))

	go func() {
		var wait = make(chan bool)
		var err1, err2 error
		var to1, to2 bool

		go func() {
			err1, to1 = bridge(proxyClient, up, c.timeout)
			wait <- true
		}()

		err2, to2 = bridge(up, proxyClient, c.timeout)
		select {
		case <-wait:
		}

		proxyClient.Close()
		up.Close()

		if to1 && to2 {
			v.Vprint(host, " unbridged due to timeout")
		} else if err1 != nil || err2 != nil {
			v.Eprint(host, " unbridged due to error: ", err1, "(down<-up) or ", err2, "(up<-down)")
		}
	}()
}

func bridge(dst, src net.Conn, t time.Duration) (err error, timedout bool) {
	buf := make([]byte, 1024*64)
	for {
		if t > 0 {
			src.SetReadDeadline(time.Now().Add(t))
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if ne, _ := er.(net.Error); ne != nil && ne.Timeout() {
				timedout = true
				break
			}
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return
}

type TTLMap struct {
	m sync.Map
}

func (m *TTLMap) String() string {
	p := bytes.Buffer{}
	m.m.Range(func(k, v interface{}) bool {
		p.WriteString(k.(string))
		p.WriteString(":")
		p.WriteString(fmt.Sprint(v))
		p.WriteString(",")
		return true
	})
	return p.String()
}

func (m *TTLMap) Add(key string, value string, ttl time.Duration) {
	if ttl == 0 {
		m.m.Store(key, value)
	} else {
		m.m.Store(key, [2]interface{}{value, time.Now().Add(ttl)})
	}
}

func (m *TTLMap) Delete(key string) {
	m.m.Delete(key)
}

func (m *TTLMap) Get(key string) (string, bool) {
	v, ok := m.m.Load(key)
	if !ok {
		return "", false
	}
	switch v := v.(type) {
	case string:
		return v, true
	case [2]interface{}:
		if time.Now().After(v[1].(time.Time)) {
			m.m.Delete(key)
			return "", false
		}
		return v[0].(string), true
	default:
		panic("shouldn't happen")
	}
}
