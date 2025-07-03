package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cokemine/ServerStatus-goclient/pkg/status"
	"github.com/gorilla/websocket"
	jsoniter "github.com/json-iterator/go"
)

var (
	SERVER   = flag.String("h", "", "Input the host of the server")
	PORT     = flag.Int("port", 35601, "Input the port of the server")
	USER     = flag.String("u", "", "Input the client's username")
	PASSWORD = flag.String("p", "", "Input the client's password")
	INTERVAL = flag.Float64("interval", 2.0, "Input the INTERVAL")
	DSN      = flag.String("dsn", "", "Input DSN, format: username:password@host:port")
	isVnstat = flag.Bool("vnstat", false, "Use vnstat for traffic statistics, linux only")
	PROTOCOL = flag.String("protocol", "tcp", "Protocol: tcp or websocket")
	USE_SSL  = flag.Bool("ssl", false, "Use SSL for websocket connection")
	PATH     = flag.String("path", "/", "WebSocket path")
	VERBOSE  = flag.Bool("verbose", false, "Enable verbose logging")
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

type ServerStatus struct {
	Uptime      uint64          `json:"uptime"`
	Load        jsoniter.Number `json:"load"`
	MemoryTotal uint64          `json:"memory_total"`
	MemoryUsed  uint64          `json:"memory_used"`
	SwapTotal   uint64          `json:"swap_total"`
	SwapUsed    uint64          `json:"swap_used"`
	HddTotal    uint64          `json:"hdd_total"`
	HddUsed     uint64          `json:"hdd_used"`
	CPU         jsoniter.Number `json:"cpu"`
	NetworkTx   uint64          `json:"network_tx"`
	NetworkRx   uint64          `json:"network_rx"`
	NetworkIn   uint64          `json:"network_in"`
	NetworkOut  uint64          `json:"network_out"`
	Online4     bool            `json:"online4"`
	Online6     bool            `json:"online6"`
}

type WebSocketConnection struct {
	conn          *websocket.Conn
	authenticated bool
}

func (ws *WebSocketConnection) ReadMessage() (string, error) {
	if ws.conn == nil {
		return "", fmt.Errorf("connection is closed")
	}

	_, message, err := ws.conn.ReadMessage()
	if err != nil {
		return "", err
	}
	return string(message), nil
}

func (ws *WebSocketConnection) WriteMessage(message string) error {
	if ws.conn == nil {
		return fmt.Errorf("connection is closed")
	}

	return ws.conn.WriteMessage(websocket.TextMessage, []byte(message))
}

func (ws *WebSocketConnection) Close() {
	ws.conn.Close()
}

func websocketConnect() (*WebSocketConnection, error) {
	// 确定协议方案
	scheme := "ws"
	if *USE_SSL {
		scheme = "wss"
	}

	// 清理主机地址
	host := *SERVER
	if strings.Contains(host, "://") {
		host = strings.Split(host, "://")[1]
	}
	host = strings.Split(host, "/")[0]

	// 构造 WebSocket URL
	u := url.URL{
		Scheme: scheme,
		Host:   fmt.Sprintf("%s:%d", host, *PORT),
		Path:   *PATH,
	}

	if *VERBOSE {
		log.Printf("Connecting to WebSocket: %s", u.String())
	}

	// 设置 WebSocket 拨号器（修复 NetDial 类型错误）
	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		NetDial: func(network, addr string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.Dial(network, addr)
		},
	}

	// 添加自定义头
	headers := make(map[string][]string)
	headers["User-Agent"] = []string{"ServerStatus-GoClient"}
	headers["Sec-WebSocket-Protocol"] = []string{"server-status"}

	// 建立连接
	conn, _, err := dialer.Dial(u.String(), headers)
	if err != nil {
		return nil, err
	}

	ws := &WebSocketConnection{
		conn:          conn,
		authenticated: false,
	}

	// 设置心跳
	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(20*time.Second))
	})

	// 设置 Pong 处理器
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(25 * time.Second))
		return nil
	})

	// 设置关闭处理
	conn.SetCloseHandler(func(code int, text string) error {
		if *VERBOSE {
			log.Printf("WebSocket closed: %d %s", code, text)
		}
		return nil
	})

	return ws, nil
}

func handleWebSocketAuthentication(ws *WebSocketConnection, disconnectChan chan struct{}) (int, error) {
	// 设置超时
	timeout := time.After(5 * time.Second)
	authenticated := false

	// 创建认证阶段的错误通道
	authErrChan := make(chan error, 1)

	// 启动读goroutine
	go func() {
		for {
			// 设置读取超时
			ws.conn.SetReadDeadline(time.Now().Add(1 * time.Second))

			message, err := ws.ReadMessage()
			if err != nil {
				// 忽略超时错误
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				authErrChan <- err
				return
			}

			if *VERBOSE {
				log.Printf("Received auth message: %s", message)
			}

			// 处理认证流程
			if strings.Contains(message, "Authentication required") {
				if *VERBOSE {
					log.Printf("Sending authentication: %s:%s", *USER, *PASSWORD)
				}
				authMsg := fmt.Sprintf("%s:%s\n", *USER, *PASSWORD)
				if err := ws.WriteMessage(authMsg); err != nil {
					authErrChan <- err
					return
				}
			} else if strings.Contains(message, "Authentication successful") {
				if *VERBOSE {
					log.Println("Authentication successful")
				}
				ws.authenticated = true
				authenticated = true
			} else if strings.Contains(message, "Authentication failed") {
				authErrChan <- fmt.Errorf("authentication failed")
				return
			} else if strings.Contains(message, "Only one connection per user allowed") {
				authErrChan <- fmt.Errorf("only one connection per user allowed")
				return
			}

			// 认证成功后检查连接类型
			if authenticated {
				if strings.Contains(message, "IPv4") {
					authErrChan <- nil // 表示成功
					return
				} else if strings.Contains(message, "IPv6") {
					authErrChan <- nil // 表示成功
					return
				} else if strings.Contains(message, "You are connecting via") {
					// 尝试从消息中提取IP类型
					if strings.Contains(message, "IPv4") {
						authErrChan <- nil // 表示成功
						return
					} else if strings.Contains(message, "IPv6") {
						authErrChan <- nil // 表示成功
						return
					}
				}
			}
		}
	}()

	// 等待认证结果
	select {
	case err := <-authErrChan:
		if err == nil {
			return 6, nil // 默认返回IPv6检查
		}
		return 0, err
	case <-timeout:
		return 0, fmt.Errorf("authentication timeout")
	case <-disconnectChan:
		return 0, fmt.Errorf("connection closed during authentication")
	}
}

func connect() {
	log.Printf("Using protocol: %s", strings.ToUpper(*PROTOCOL))

	if *PROTOCOL == "websocket" {
		connectWebSocket()
	} else {
		connectTCP()
	}
}

func connectWebSocket() {
	// 指数退避重连策略
	reconnectDelay := 3 * time.Second
	maxReconnectDelay := 30 * time.Second

	// 主连接循环
	for {
		log.Println("Connecting via WebSocket...")
		startTime := time.Now() // 记录连接开始时间

		// 建立 WebSocket 连接
		ws, err := websocketConnect()
		if err != nil {
			log.Printf("WebSocket connection failed: %v", err)
			time.Sleep(reconnectDelay)
			// 指数退避
			reconnectDelay = minDuration(reconnectDelay*2, maxReconnectDelay)
			continue
		}

		if *VERBOSE {
			log.Printf("WebSocket connected in %v, waiting for authentication...", time.Since(startTime))
		}

		// 创建断开信号通道
		disconnectChan := make(chan struct{}, 2)

		// 处理认证
		checkIP, err := handleWebSocketAuthentication(ws, disconnectChan)
		if err != nil {
			log.Printf("Authentication failed: %v", err)
			ws.Close()
			time.Sleep(reconnectDelay)
			// 指数退避
			reconnectDelay = minDuration(reconnectDelay*2, maxReconnectDelay)
			continue
		}

		if *VERBOSE {
			log.Println("Authentication successful, starting data reporting")
		}

		// 重置重连延迟（连接成功后）
		reconnectDelay = 3 * time.Second

		// 启动心跳检测
		heartbeatTicker := time.NewTicker(15 * time.Second)
		heartbeatFailCount := 0
		maxHeartbeatFailCount := 2

		// 启动读错误检测
		readErrChan := make(chan error, 1)
		go func() {
			for {
				ws.conn.SetReadDeadline(time.Now().Add(25 * time.Second))
				_, _, err := ws.conn.ReadMessage()
				if err != nil {
					readErrChan <- err
					return
				}
			}
		}()

		// 主循环：收集系统信息并发送
		timer := 0.0
		item := ServerStatus{}
		writeFailCount := 0
		maxWriteFailCount := 2

		// 快速重连标志
		reconnectRequested := false

	connectionLoop:
		for {
			select {
			case <-disconnectChan:
				log.Println("Disconnect signal received, reconnecting...")
				reconnectRequested = true
				break connectionLoop

			case err := <-readErrChan:
				if *VERBOSE {
					log.Printf("Read error: %v, triggering reconnect", err)
				}
				reconnectRequested = true
				break connectionLoop

			case <-heartbeatTicker.C:
				ws.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if err := ws.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					heartbeatFailCount++
					if *VERBOSE {
						log.Printf("Ping failed (%d/%d): %v", heartbeatFailCount, maxHeartbeatFailCount, err)
					}

					if heartbeatFailCount >= maxHeartbeatFailCount {
						log.Println("Max ping failures reached, triggering reconnect")
						disconnectChan <- struct{}{}
					}
				} else {
					heartbeatFailCount = 0
					if *VERBOSE {
						log.Println("Ping sent successfully")
					}
				}

			case <-time.After(time.Duration(*INTERVAL*1000) * time.Millisecond):
				// 收集系统信息
				CPU := status.Cpu(*INTERVAL)
				var netIn, netOut, netRx, netTx uint64
				if !*isVnstat {
					netIn, netOut, netRx, netTx = status.Traffic(*INTERVAL)
				} else {
					_, _, netRx, netTx = status.Traffic(*INTERVAL)
					netIn, netOut, err = status.TrafficVnstat()
					if err != nil {
						log.Println("Please check if the installation of vnStat is correct")
					}
				}
				memoryTotal, memoryUsed, swapTotal, swapUsed := status.Memory()
				hddTotal, hddUsed := status.Disk(*INTERVAL)
				uptime := status.Uptime()
				load := status.Load()

				// 填充数据结构
				item.CPU = jsoniter.Number(fmt.Sprintf("%.1f", CPU))
				item.Load = jsoniter.Number(fmt.Sprintf("%.2f", load))
				item.Uptime = uptime
				item.MemoryTotal = memoryTotal
				item.MemoryUsed = memoryUsed
				item.SwapTotal = swapTotal
				item.SwapUsed = swapUsed
				item.HddTotal = hddTotal
				item.HddUsed = hddUsed
				item.NetworkRx = netRx
				item.NetworkTx = netTx
				item.NetworkIn = netIn
				item.NetworkOut = netOut

				// 定期检查网络连通性
				if timer <= 0 {
					if checkIP == 4 {
						item.Online4 = status.Network(checkIP)
					} else if checkIP == 6 {
						item.Online6 = status.Network(checkIP)
					}
					timer = 150.0
				}
				timer -= *INTERVAL

				// 序列化并发送数据
				data, _ := json.Marshal(item)
				message := fmt.Sprintf("update %s\n", string(data))
				if *VERBOSE {
					log.Printf("Sending: %s", strings.TrimSpace(message))
				}

				// 尝试发送数据，设置更短的写超时
				ws.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
				if err := ws.conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
					writeFailCount++
					log.Printf("Write failed (%d/%d): %v", writeFailCount, maxWriteFailCount, err)

					if writeFailCount >= maxWriteFailCount {
						log.Println("Max write failures reached, triggering reconnect")
						disconnectChan <- struct{}{}
					}
				} else {
					writeFailCount = 0
				}
			}

			if reconnectRequested {
				break connectionLoop
			}
		}

		// 停止心跳ticker
		heartbeatTicker.Stop()

		// 确保连接关闭
		ws.Close()

		// 等待重连
		log.Printf("Reconnecting in %v...", reconnectDelay)
		time.Sleep(reconnectDelay)

		// 指数退避
		reconnectDelay = minDuration(reconnectDelay*2, maxReconnectDelay)
	}
}

// 辅助函数：取两个时间的最小值
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func connectTCP() {
	log.Println("Connecting via TCP...")

	conn, err := net.DialTimeout("tcp", *SERVER+":"+strconv.Itoa(*PORT), 30*time.Second)
	if err != nil {
		log.Println("Caught Exception:", err.Error())
		time.Sleep(5 * time.Second)
		return
	}
	defer func(conn net.Conn) {
		_ = conn.Close()
		time.Sleep(5 * time.Second)
	}(conn)

	var buf = make([]byte, 128)
	var data = status.BytesToString(buf)
	n, err := conn.Read(buf)
	if err != nil {
		log.Println("Caught Exception:", err.Error())
		return
	}

	log.Println(data[:n])
	if !strings.Contains(data, "Authentication required") || err != nil {
		return
	} else {
		_, _ = conn.Write([]byte((*USER + ":" + *PASSWORD + "\n")))
	}

	n, _ = conn.Read(buf)
	log.Println(data[:n])
	if !strings.Contains(data, "Authentication successful") {
		return
	}

	if !strings.Contains(data, "You are connecting via") {
		n, _ = conn.Read(buf)
		log.Println(data[:n])
	}

	timer := 0.0
	checkIP := 0
	if strings.Contains(data, "IPv4") {
		checkIP = 6
	} else if strings.Contains(data, "IPv6") {
		checkIP = 4
	} else {
		return
	}

	item := ServerStatus{}
	for {
		CPU := status.Cpu(*INTERVAL)
		var netIn, netOut, netRx, netTx uint64
		if !*isVnstat {
			netIn, netOut, netRx, netTx = status.Traffic(*INTERVAL)
		} else {
			_, _, netRx, netTx = status.Traffic(*INTERVAL)
			netIn, netOut, err = status.TrafficVnstat()
			if err != nil {
				log.Println("Please check if the installation of vnStat is correct")
			}
		}
		memoryTotal, memoryUsed, swapTotal, swapUsed := status.Memory()
		hddTotal, hddUsed := status.Disk(*INTERVAL)
		uptime := status.Uptime()
		load := status.Load()

		item.CPU = jsoniter.Number(fmt.Sprintf("%.1f", CPU))
		item.Load = jsoniter.Number(fmt.Sprintf("%.2f", load))
		item.Uptime = uptime
		item.MemoryTotal = memoryTotal
		item.MemoryUsed = memoryUsed
		item.SwapTotal = swapTotal
		item.SwapUsed = swapUsed
		item.HddTotal = hddTotal
		item.HddUsed = hddUsed
		item.NetworkRx = netRx
		item.NetworkTx = netTx
		item.NetworkIn = netIn
		item.NetworkOut = netOut

		if timer <= 0 {
			if checkIP == 4 {
				item.Online4 = status.Network(checkIP)
			} else if checkIP == 6 {
				item.Online6 = status.Network(checkIP)
			}
			timer = 150.0
		}
		timer -= *INTERVAL

		data, _ := json.Marshal(item)
		_, err = conn.Write(status.StringToBytes("update " + status.BytesToString(data) + "\n"))
		if err != nil {
			log.Println(err.Error())
			break
		}

		time.Sleep(time.Duration(*INTERVAL*1000) * time.Millisecond)
	}
}

func main() {
	flag.Parse()

	if *DSN != "" {
		if strings.Contains(*DSN, "://") {
			// 正确格式: protocol://username:password@host:port/path?query
			u, err := url.Parse(*DSN)
			if err != nil {
				log.Fatalf("Invalid DSN format: %v", err)
			}

			// 提取用户名和密码
			if u.User != nil {
				*USER = u.User.Username()
				if pwd, ok := u.User.Password(); ok {
					*PASSWORD = pwd
				}
			}

			// 设置协议
			switch u.Scheme {
			case "ws":
				*PROTOCOL = "websocket"
				*USE_SSL = false
			case "wss":
				*PROTOCOL = "websocket"
				*USE_SSL = true
			case "http":
				*PROTOCOL = "tcp"
				*USE_SSL = false
			case "https":
				*PROTOCOL = "tcp"
				*USE_SSL = true
			default:
				*PROTOCOL = strings.ToLower(u.Scheme)
			}

			// 设置服务器和端口
			*SERVER = u.Hostname()
			if portStr := u.Port(); portStr != "" {
				port, err := strconv.Atoi(portStr)
				if err != nil {
					log.Fatal("Invalid port in DSN")
				}
				*PORT = port
			}

			// 设置WebSocket路径
			if u.Path != "" {
				*PATH = u.Path
			}
		} else {
			// 传统格式: username:password@host:port
			dsnParts := strings.Split(*DSN, "@")
			if len(dsnParts) != 2 {
				log.Fatal("Invalid DSN format. Should be: username:password@host:port")
			}

			prev := strings.Split(dsnParts[0], ":")
			if len(prev) != 2 {
				log.Fatal("Invalid DSN format. Should be: username:password@host:port")
			}

			next := strings.Split(dsnParts[1], ":")
			*USER = prev[0]
			*PASSWORD = prev[1]
			*SERVER = next[0]

			if len(next) == 2 {
				port, err := strconv.Atoi(next[1])
				if err != nil {
					log.Fatal("Invalid port in DSN")
				}
				*PORT = port
			}
		}
	}

	// 验证端口范围
	if *PORT < 1 || *PORT > 65535 {
		log.Fatal("Check the port you input")
	}

	// 验证必要参数
	if *SERVER == "" || *USER == "" || *PASSWORD == "" {
		log.Fatal("HOST, USERNAME, PASSWORD must not be blank!")
	}

	// 验证协议选项
	if *PROTOCOL != "tcp" && *PROTOCOL != "websocket" {
		log.Fatal("Protocol must be either 'tcp' or 'websocket'")
	}

	// 确保路径格式正确
	if !strings.HasPrefix(*PATH, "/") {
		*PATH = "/" + *PATH
	}

	// 主循环
	for {
		connect()
	}
}
