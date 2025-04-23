package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/coyove/common/sched"
	"github.com/coyove/goflyway"
	"github.com/coyove/goflyway/v"
	"golang.org/x/net/websocket"
)

var (
	goflywayServerPort = flag.String("goflyway-server-port", "8000", "goflyway server port")
	goflywayClientPort = flag.String("goflyway-client-port", "1080", "goflyway client port")
	wsServerPort       = flag.String("ws-server-port", "8080", "WebSocket server port")
	testKey            = flag.String("key", "test-key", "Encryption key for test")
)

// 直接使用 cmd/goflyway/main.go 中的方法启动服务器
func startServer(addr string, key string) error {
	sconfig := &goflyway.ServerConfig{}
	sconfig.Key = key
	sconfig.WriteBuffer = 64 * 1024

	v.Vprint("server listen on", addr)
	return goflyway.NewServer(addr, sconfig)
}

// 直接使用 cmd/goflyway/main.go 中的方法启动客户端
func startClient(localAddr, remoteAddr, serverAddr string, key string, webSocket bool) error {
	cconfig := &goflyway.ClientConfig{}
	cconfig.Key = key
	cconfig.WebSocket = webSocket
	cconfig.Stat = &goflyway.Traffic{}
	cconfig.WriteBuffer = 64 * 1024
	cconfig.Upstream = serverAddr
	cconfig.Bind = remoteAddr

	v.Vprint("forward", localAddr, "to", remoteAddr, "through", serverAddr)
	if webSocket {
		v.Vprint("relay: use Websocket protocol")
	}

	return goflyway.NewClient(localAddr, cconfig)
}

func TestE2E(t *testing.T) {
	// 初始化
	flag.Parse()
	v.Verbose = 2
	sched.Verbose = false

	// 启动 WebSocket 服务器
	wsServer := startWebSocketServer(t)
	defer wsServer.Shutdown(context.Background())

	// 准备地址
	serverAddr := ":" + *goflywayServerPort
	localAddr := ":" + *goflywayClientPort
	remoteAddr := "127.0.0.1:" + *wsServerPort

	// 启动 goflyway 服务器
	serverErr := make(chan error, 1)
	go func() {
		t.Logf("Starting goflyway server on port %s", *goflywayServerPort)
		if err := startServer(serverAddr, *testKey); err != nil {
			serverErr <- fmt.Errorf("goflyway server error: %v", err)
		}
	}()

	// 等待服务器启动
	time.Sleep(1 * time.Second)

	// 启动 goflyway 客户端
	clientErr := make(chan error, 1)
	go func() {
		t.Logf("Starting goflyway client on port %s", *goflywayClientPort)
		serverFullAddr := "127.0.0.1" + serverAddr
		if err := startClient(localAddr, remoteAddr, serverFullAddr, *testKey, true); err != nil {
			clientErr <- fmt.Errorf("goflyway client error: %v", err)
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
	t.Logf("Connecting to WebSocket at ws://localhost:%s/ws", *goflywayClientPort)
	wsClient := connectWebSocketClient(t)
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

func connectWebSocketClient(t *testing.T) *websocket.Conn {
	url := fmt.Sprintf("ws://localhost:%s/ws", *goflywayClientPort)
	t.Logf("Creating WebSocket config for %s", url)
	config, err := websocket.NewConfig(url, "http://localhost")
	if err != nil {
		t.Fatalf("Failed to create WebSocket config: %v", err)
	}

	conn, err := websocket.DialConfig(config)
	if err != nil {
		t.Fatalf("Failed to connect WebSocket: %v", err)
	}

	return conn
}
