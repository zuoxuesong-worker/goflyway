package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coyove/common/sched"
	"github.com/edgewize-io/regulaway"
	"github.com/edgewize-io/regulaway/v"
	"golang.org/x/net/websocket"
)

var (
	regulawayServerPort = flag.String("regulaway-server-port", "8000", "regulaway server port")
	regulawayClientPort = flag.String("regulaway-client-port", "1080", "regulaway client port")
	wsServerPort        = flag.String("ws-server-port", "8080", "WebSocket server port")
	testKey             = flag.String("key", "test-key", "Encryption key for test")

	// 极限测试参数
	concurrentConns = flag.Int("conns", 100, "Number of concurrent connections")
	msgPerConn      = flag.Int("msgs", 10000, "Number of messages per connection")
	msgSize         = flag.Int("size", 1024, "Message size in bytes")
	testDuration    = flag.Duration("duration", 30*time.Second, "Test duration")
	reportInterval  = flag.Duration("report", 1*time.Second, "Report interval (default 1s)")

	// 标准错误输出，确保日志始终可见
	stderr = os.Stderr
)

// log打印到标准错误，确保在测试中可见
func logf(format string, args ...interface{}) {
	fmt.Fprintf(stderr, format+"\n", args...)
}

// 测试统计
type testStats struct {
	sentMessages      int64
	receivedMessages  int64
	errors            int64
	bytesTransferred  int64
	activeConns       int64
	totalConnAttempts int64
	connSuccess       int64
	connFailed        int64
	latencySum        int64 // 总延迟(微秒)
	latencyCount      int64 // 延迟计数
}

// 服务器统计
type serverStats struct {
	activeConns       int64
	totalConns        int64
	messagesProcessed int64
	bytesProcessed    int64
}

var (
	clientStats = &testStats{}
	srvStats    = &serverStats{}
)

// 生成随机消息
func generateRandomMessage(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, size)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// 直接使用 cmd/regulaway/main.go 中的方法启动服务器
func startServer(addr string, key string) error {
	sconfig := &regulaway.ServerConfig{}
	sconfig.Key = key
	sconfig.WriteBuffer = 64 * 1024 * 4 // 增加缓冲区大小以处理高并发

	v.Vprint("server listen on", addr)
	return regulaway.NewServer(addr, sconfig)
}

// 直接使用 cmd/regulaway/main.go 中的方法启动客户端
func startClient(localAddr, remoteAddr, serverAddr string, key string, webSocket bool) error {
	cconfig := &regulaway.ClientConfig{}
	cconfig.Key = key
	cconfig.WebSocket = webSocket
	cconfig.Stat = &regulaway.Traffic{}
	cconfig.WriteBuffer = 64 * 1024 * 4 // 增加缓冲区大小以处理高并发
	cconfig.Upstream = serverAddr
	cconfig.Bind = remoteAddr

	v.Vprint("forward", localAddr, "to", remoteAddr, "through", serverAddr)
	if webSocket {
		v.Vprint("relay: use Websocket protocol")
	}

	return regulaway.NewClient(localAddr, cconfig)
}

func TestBasicE2E(t *testing.T) {
	// 初始化
	flag.Parse()
	v.Verbose = 1 // 降低日志级别以减少噪音
	sched.Verbose = false

	// 启动 WebSocket 服务器
	wsServer := startWebSocketServer(t)
	defer wsServer.Shutdown(context.Background())

	// 准备地址
	serverAddr := ":" + *regulawayServerPort
	localAddr := ":" + *regulawayClientPort
	remoteAddr := "127.0.0.1:" + *wsServerPort

	// 启动 regulaway 服务器
	serverErr := make(chan error, 1)
	go func() {
		t.Logf("Starting regulaway server on port %s", *regulawayServerPort)
		if err := startServer(serverAddr, *testKey); err != nil {
			serverErr <- fmt.Errorf("regulaway server error: %v", err)
		}
	}()

	// 等待服务器启动
	time.Sleep(1 * time.Second)

	// 启动 regulaway 客户端
	clientErr := make(chan error, 1)
	go func() {
		t.Logf("Starting regulaway client on port %s", *regulawayClientPort)
		serverFullAddr := "127.0.0.1" + serverAddr
		if err := startClient(localAddr, remoteAddr, serverFullAddr, *testKey, true); err != nil {
			clientErr <- fmt.Errorf("regulaway client error: %v", err)
		}
	}()

	// 等待服务启动
	time.Sleep(1 * time.Second)

	// 检查是否有启动错误
	select {
	case err := <-serverErr:
		t.Fatalf("Server failed to start: %v", err)
	case err := <-clientErr:
		t.Fatalf("Client failed to start: %v", err)
	default:
	}

	// 连接 WebSocket 客户端
	t.Logf("Connecting to WebSocket at ws://localhost:%s/ws", *regulawayClientPort)
	wsClient, err := connectWebSocketClient(t)
	if err != nil {
		t.Fatalf("Failed to connect WebSocket client: %v", err)
	}
	defer wsClient.Close()

	// 测试发送和接收消息
	testMessages := []string{
		"test message 1",
		"test message 2",
		"test message 3",
	}

	for _, msg := range testMessages {
		// 发送消息
		if err := websocket.Message.Send(wsClient, msg); err != nil {
			t.Fatalf("Failed to send message: %v", err)
		}

		// 接收回应
		var response string
		if err := websocket.Message.Receive(wsClient, &response); err != nil {
			t.Fatalf("Failed to receive message: %v", err)
		}

		// 验证回应
		expected := "Echo: " + msg
		if response != expected {
			t.Errorf("Expected %q, got %q", expected, response)
		} else {
			t.Logf("Received message: %q", response)
		}
	}
}

func TestStressE2E(t *testing.T) {
	// 始终输出日志到用户
	t.Logf("NOTE: 要看到实时输出，请使用 go test -v example/e2e_test/... -count=1")

	// 初始化
	flag.Parse()
	v.Verbose = 1 // 设置为1以确保看到基本日志
	sched.Verbose = false
	rand.Seed(time.Now().UnixNano())

	// 重置统计
	clientStats = &testStats{}
	srvStats = &serverStats{}

	logf("==== STARTING STRESS TEST ====")
	logf("Connections: %d, Messages per conn: %d, Message size: %d bytes",
		*concurrentConns, *msgPerConn, *msgSize)
	logf("Test duration: %v, Report interval: %v", *testDuration, *reportInterval)

	// 使日志对用户可见
	t.Logf("Test started with %d connections, %d msgs/connection, %d byte messages",
		*concurrentConns, *msgPerConn, *msgSize)

	// 设置超时上下文，确保测试不会无限期运行
	ctx, cancel := context.WithTimeout(context.Background(), *testDuration)
	defer cancel()

	// 启动 WebSocket 服务器
	wsServer := startStressWebSocketServer(t)
	defer wsServer.Shutdown(context.Background())

	// 准备地址
	serverAddr := ":" + *regulawayServerPort
	localAddr := ":" + *regulawayClientPort
	remoteAddr := "127.0.0.1:" + *wsServerPort

	// 启动 regulaway 服务器
	serverErr := make(chan error, 1)
	go func() {
		logf("Starting regulaway server on port %s", *regulawayServerPort)
		if err := startServer(serverAddr, *testKey); err != nil {
			serverErr <- fmt.Errorf("regulaway server error: %v", err)
		}
	}()

	// 等待服务器启动
	time.Sleep(1 * time.Second)

	// 启动 regulaway 客户端
	clientErr := make(chan error, 1)
	go func() {
		logf("Starting regulaway client on port %s", *regulawayClientPort)
		serverFullAddr := "127.0.0.1" + serverAddr
		if err := startClient(localAddr, remoteAddr, serverFullAddr, *testKey, true); err != nil {
			clientErr <- fmt.Errorf("regulaway client error: %v", err)
		}
	}()

	// 等待服务启动
	time.Sleep(1 * time.Second)

	// 检查是否有启动错误
	select {
	case err := <-serverErr:
		t.Fatalf("Server failed to start: %v", err)
	case err := <-clientErr:
		t.Fatalf("Client failed to start: %v", err)
	default:
		logf("Both server and client started successfully")
	}

	// 启动统计报告协程
	stopReport := make(chan struct{})
	go reportStats(t, stopReport)

	// 启动并发连接
	var wg sync.WaitGroup
	wg.Add(*concurrentConns)

	logf("Starting %d concurrent connections...", *concurrentConns)
	t.Logf("Starting %d concurrent connections", *concurrentConns)

	for i := 0; i < *concurrentConns; i++ {
		go func(connID int) {
			defer wg.Done()

			// 随机延迟以避免所有连接同时建立
			time.Sleep(time.Duration(rand.Intn(1000)) * time.Millisecond)

			// 创建连接
			atomic.AddInt64(&clientStats.totalConnAttempts, 1)
			atomic.AddInt64(&clientStats.activeConns, 1)
			defer atomic.AddInt64(&clientStats.activeConns, -1)

			conn, err := connectWebSocketClient(t)
			if err != nil {
				logf("Failed to connect client %d: %v", connID, err)
				atomic.AddInt64(&clientStats.errors, 1)
				atomic.AddInt64(&clientStats.connFailed, 1)
				return
			}
			atomic.AddInt64(&clientStats.connSuccess, 1)
			defer conn.Close()

			// 发送和接收消息
			msgCount := 0
			for msgCount < *msgPerConn {
				select {
				case <-ctx.Done():
					// 测试时间到，退出
					return
				default:
					// 继续测试
				}

				// 生成随机消息
				msg := generateRandomMessage(*msgSize)

				// 发送消息，记录时间
				startTime := time.Now()
				if err := websocket.Message.Send(conn, msg); err != nil {
					logf("Send error on conn %d: %v", connID, err)
					atomic.AddInt64(&clientStats.errors, 1)
					return
				}
				atomic.AddInt64(&clientStats.sentMessages, 1)
				atomic.AddInt64(&clientStats.bytesTransferred, int64(len(msg)))

				// 接收响应
				var response string
				if err := websocket.Message.Receive(conn, &response); err != nil {
					logf("Receive error on conn %d: %v", connID, err)
					atomic.AddInt64(&clientStats.errors, 1)
					return
				}

				// 计算延迟（微秒）
				latency := time.Since(startTime).Microseconds()
				atomic.AddInt64(&clientStats.latencySum, latency)
				atomic.AddInt64(&clientStats.latencyCount, 1)

				atomic.AddInt64(&clientStats.receivedMessages, 1)
				atomic.AddInt64(&clientStats.bytesTransferred, int64(len(response)))

				msgCount++

				// 随机延迟以模拟真实场景
				time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
			}
		}(i)
	}

	// 等待测试完成或超时
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logf("All connections completed")
	case <-ctx.Done():
		logf("Test duration reached")
	}

	// 停止统计报告
	close(stopReport)

	// 最终报告
	printFinalStats(t)

	// 确保测试结果被看到
	t.Logf("Test completed - 测试已完成!")
}

func reportStats(t *testing.T, stop chan struct{}) {
	ticker := time.NewTicker(*reportInterval)
	defer ticker.Stop()

	var lastSent, lastReceived, lastBytes int64
	var lastSrvMsgs, lastSrvBytes int64
	startTime := time.Now()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			elapsedSec := now.Sub(startTime).Seconds()

			// 客户端统计
			currentSent := atomic.LoadInt64(&clientStats.sentMessages)
			currentReceived := atomic.LoadInt64(&clientStats.receivedMessages)
			currentBytes := atomic.LoadInt64(&clientStats.bytesTransferred)
			currentErrors := atomic.LoadInt64(&clientStats.errors)
			currentConns := atomic.LoadInt64(&clientStats.activeConns)
			totalAttempts := atomic.LoadInt64(&clientStats.totalConnAttempts)
			connSuccess := atomic.LoadInt64(&clientStats.connSuccess)
			connFailed := atomic.LoadInt64(&clientStats.connFailed)

			// 计算客户端速率
			sentRate := float64(currentSent-lastSent) / float64(*reportInterval/time.Second)
			receivedRate := float64(currentReceived-lastReceived) / float64(*reportInterval/time.Second)
			bytesRate := float64(currentBytes-lastBytes) / float64(*reportInterval/time.Second) / 1024 / 1024 // MB/s

			// 服务器统计
			srvMsgs := atomic.LoadInt64(&srvStats.messagesProcessed)
			srvBytes := atomic.LoadInt64(&srvStats.bytesProcessed)
			srvConns := atomic.LoadInt64(&srvStats.activeConns)
			srvTotalConns := atomic.LoadInt64(&srvStats.totalConns)

			// 计算服务器速率
			srvMsgRate := float64(srvMsgs-lastSrvMsgs) / float64(*reportInterval/time.Second)
			srvBytesRate := float64(srvBytes-lastSrvBytes) / float64(*reportInterval/time.Second) / 1024 / 1024 // MB/s

			// 计算平均延迟
			latencySum := atomic.LoadInt64(&clientStats.latencySum)
			latencyCount := atomic.LoadInt64(&clientStats.latencyCount)
			avgLatency := float64(0)
			if latencyCount > 0 {
				avgLatency = float64(latencySum) / float64(latencyCount) / 1000 // 毫秒
			}

			// 同时打印到标准错误和测试日志
			status := fmt.Sprintf("=== %.1fs === C:%d↑%.0f↓%.0f MB:%.2f ERR:%d Latency:%.2fms | S:%d REQ:%.0f MB:%.2f",
				elapsedSec, currentConns, sentRate, receivedRate, bytesRate, currentErrors, avgLatency,
				srvConns, srvMsgRate, srvBytesRate)

			// 输出到测试日志（会在-v标志下显示）
			t.Logf(status)

			// 输出到标准错误（始终显示）
			logf("=== %.1fs ===", elapsedSec)
			logf("CLIENT: Conns: %d/%d (act/tot), Success: %d, Failed: %d, Msgs: %d→%d, Rate: %.1f→%.1f msg/s, BW: %.2f MB/s, Errors: %d, Latency: %.2f ms",
				currentConns, totalAttempts, connSuccess, connFailed,
				currentSent, currentReceived, sentRate, receivedRate, bytesRate, currentErrors, avgLatency)
			logf("SERVER: Conns: %d/%d (act/tot), Msgs: %d, Rate: %.1f msg/s, BW: %.2f MB/s",
				srvConns, srvTotalConns, srvMsgs, srvMsgRate, srvBytesRate)

			// 更新上次的值
			lastSent = currentSent
			lastReceived = currentReceived
			lastBytes = currentBytes
			lastSrvMsgs = srvMsgs
			lastSrvBytes = srvBytes

		case <-stop:
			return
		}
	}
}

func printFinalStats(t *testing.T) {
	// 客户端统计
	sent := atomic.LoadInt64(&clientStats.sentMessages)
	received := atomic.LoadInt64(&clientStats.receivedMessages)
	bytes := atomic.LoadInt64(&clientStats.bytesTransferred)
	errors := atomic.LoadInt64(&clientStats.errors)
	connSuccess := atomic.LoadInt64(&clientStats.connSuccess)
	connFailed := atomic.LoadInt64(&clientStats.connFailed)

	// 服务器统计
	srvMsgs := atomic.LoadInt64(&srvStats.messagesProcessed)
	srvBytes := atomic.LoadInt64(&srvStats.bytesProcessed)
	srvTotalConns := atomic.LoadInt64(&srvStats.totalConns)

	// 计算延迟
	latencySum := atomic.LoadInt64(&clientStats.latencySum)
	latencyCount := atomic.LoadInt64(&clientStats.latencyCount)
	avgLatency := float64(0)
	if latencyCount > 0 {
		avgLatency = float64(latencySum) / float64(latencyCount) / 1000 // 毫秒
	}

	summary := fmt.Sprintf("TEST SUMMARY: Conns: %d, Msgs: %d→%d, Bytes: %.2fMB, Errors: %d, Success: %.2f%%, Latency: %.2fms",
		connSuccess, sent, received, float64(bytes)/(1024*1024), errors,
		float64(received)/float64(sent)*100, avgLatency)

	// 输出到测试日志
	t.Logf(summary)

	// 输出到标准错误
	logf("===== TEST COMPLETED =====")
	logf("CLIENT STATS:")
	logf("  Total Connection Attempts: %d", clientStats.totalConnAttempts)
	logf("  Successful Connections:    %d", connSuccess)
	logf("  Failed Connections:        %d", connFailed)
	logf("  Total Messages Sent:       %d", sent)
	logf("  Total Messages Received:   %d", received)
	logf("  Total Bytes Transferred:   %.2f MB", float64(bytes)/(1024*1024))
	logf("  Total Errors:              %d", errors)
	logf("  Message Success Rate:      %.2f%%", float64(received)/float64(sent)*100)
	logf("  Average Latency:           %.2f ms", avgLatency)

	logf("SERVER STATS:")
	logf("  Total Connections:         %d", srvTotalConns)
	logf("  Total Messages Processed:  %d", srvMsgs)
	logf("  Total Bytes Processed:     %.2f MB", float64(srvBytes)/(1024*1024))
}

func startStressWebSocketServer(t *testing.T) *http.Server {
	server := &http.Server{
		Addr: ":" + *wsServerPort,
		// 增加超时设置以处理大量连接
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// 使用更高效的处理程序
	http.Handle("/ws", websocket.Handler(func(ws *websocket.Conn) {
		// 增加缓冲区大小
		ws.MaxPayloadBytes = 1 << 20 // 1MB

		// 记录连接状态
		atomic.AddInt64(&srvStats.activeConns, 1)
		atomic.AddInt64(&srvStats.totalConns, 1)
		defer atomic.AddInt64(&srvStats.activeConns, -1)

		for {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				return
			}

			// 更新服务器统计
			msgLen := int64(len(msg))
			atomic.AddInt64(&srvStats.messagesProcessed, 1)
			atomic.AddInt64(&srvStats.bytesProcessed, msgLen)

			// 直接回显，不做额外处理以提高性能
			response := "Echo: " + msg
			if err := websocket.Message.Send(ws, response); err != nil {
				return
			}

			// 计算响应字节
			respLen := int64(len(response))
			atomic.AddInt64(&srvStats.bytesProcessed, respLen)
		}
	}))

	go func() {
		logf("Starting WebSocket server on port %s", *wsServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logf("WebSocket server error: %v", err)
		}
	}()

	return server
}

func startWebSocketServer(t *testing.T) *http.Server {
	server := &http.Server{
		Addr: ":" + *wsServerPort,
	}

	http.Handle("/ws", websocket.Handler(func(ws *websocket.Conn) {
		for {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				return
			}
			if err := websocket.Message.Send(ws, "Echo: "+msg); err != nil {
				return
			}
		}
	}))

	go func() {
		t.Logf("Starting WebSocket server on port %s", *wsServerPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("WebSocket server error: %v", err)
		}
	}()

	return server
}

func connectWebSocketClient(t testing.TB) (*websocket.Conn, error) {
	url := fmt.Sprintf("ws://localhost:%s/ws", *regulawayClientPort)
	if testing.Verbose() {
		t.Logf("Creating WebSocket config for %s", url)
	}

	config, err := websocket.NewConfig(url, "http://localhost")
	if err != nil {
		return nil, fmt.Errorf("failed to create WebSocket config: %v", err)
	}

	// 增加超时设置
	config.Header.Add("Origin", "http://localhost")

	conn, err := websocket.DialConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to connect WebSocket: %v", err)
	}

	return conn, nil
}
