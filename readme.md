# goflyway v2 - 基于HTTP的本地端口转发工具

![](https://raw.githubusercontent.com/coyove/goflyway/gdev/.misc/logo.png)

`master` 分支是活跃的开发分支，包含v2版本代码。稳定的v1版本（曾被称为v2.0）请参考 [v1.0分支](https://github.com/coyove/goflyway/tree/v1.0)。

goflyway v2 是一个特殊的工具，可以安全地将本地端口转发到远程服务器，类似于 `ssh -L` 功能。

goflyway 使用纯HTTP POST请求来中继TCP连接。这里不需要也不涉及CONNECT方法，因为goflyway主要设计用于那些处于无CONNECT方法的HTTP代理后面的用户，或者希望通过静态CDN加速连接的用户。

然而，如果您已经有更好的网络环境，纯HTTP请求无疑会浪费带宽。因此，可以使用 `-w` 选项开启WebSocket中继，或者在可能的情况下使用 `-K` 开启KCP中继。

## 基本用法
通过 `server:80` 将 `localhost:1080` 转发到 `server:1080`

```
    服务器: ./goflyway :80
    客户端: ./goflyway -L 1080::1080 server:80 -p password
```

通过 `server:80` 使用WebSocket将 `localhost:1080` 转发到 `server2:1080`

```
    服务器: ./goflyway :80
    客户端: ./goflyway -w -L 1080:server2:1080 server:80 -p password
```

动态转发 `localhost:1080` 到 `server:80`

```
    服务器: ./goflyway :80
    客户端: ./goflyway -D 1080 server:80 -p password
```

在同一端口上的HTTP反向代理或静态文件服务器：

```
    ./goflyway :80 -P http://127.0.0.1:8080 
    ./goflyway :80 -P /var/www/html
```

## 写入缓冲区

在HTTP模式下，当服务器接收到数据时，无法直接发送给客户端，因为HTTP不是双向的。服务器必须等待客户端请求这些数据，这意味着这些数据会在内存中存储一段时间。

您可以使用 `-W bytes` 限制服务器可以缓冲的最大字节数（每个连接），默认值是1048576（1M）。如果缓冲区达到限制，后续字节将被阻塞，直到缓冲区有空闲空间。

## 测试方法

goflyway 提供了两种测试方法：手动测试和自动化测试。

### 1. 手动测试流程

手动测试允许您单独启动每个组件并观察其行为，适合调试和功能验证。

#### 步骤 1: 启动 WebSocket 回显服务器

首先，我们需要一个简单的 WebSocket 服务器用于测试。可以使用提供的测试服务器或自己实现一个：

```bash
# 在端口 8080 启动 WebSocket 回显服务器
go run example/websocket_echo_server/main.go
```

#### 步骤 2: 启动 goflyway 服务器

在另一个终端中，启动 goflyway 服务器：

```bash
# 在端口 8100 启动服务器
./goflyway -k testkey -w :8100
```

参数说明：
- `-k testkey`: 设置密钥为 "testkey"
- `-w`: 启用 WebSocket 模式
- `:8100`: 监听 8100 端口

#### 步骤 3: 启动 goflyway 客户端

在第三个终端中，启动 goflyway 客户端：

```bash
# 将本地 1080 端口的流量通过代理转发到 WebSocket 服务器
./goflyway -k testkey -w -L :1080 -D 127.0.0.1:8100
```

参数说明：
- `-k testkey`: 使用与服务器相同的密钥
- `-w`: 启用 WebSocket 模式
- `-L :1080`: 在本地 1080 端口监听
- `-D`: 启用动态模式
- `127.0.0.1:8100`: 服务器地址

#### 步骤 4: 测试连接

使用 WebSocket 客户端（如 wscat）通过代理连接到回显服务器：

```bash
# 通过 goflyway 客户端连接到 WebSocket 服务器
wscat -c ws://localhost:1080/ws
```

现在可以在 wscat 中输入消息，并验证是否收到回显响应。

#### 步骤 5: 检查性能和稳定性

- 监控 CPU 和内存使用情况
- 检查日志中是否有错误或异常
- 进行长时间运行测试，确保稳定性

### 2. 自动化测试流程

goflyway 提供了自动化测试用例，可以自动设置和测试整个系统。

#### 运行基本功能测试

```bash
go test -v -run TestBasicE2E example/e2e_test/e2e_test.go
```

这会运行基本的端到端测试，验证 goflyway 的核心功能。

#### 运行压力测试

对于性能和稳定性测试，可以使用压力测试模式：

```bash
go test -v -run TestStressE2E example/e2e_test/e2e_test.go -count=1
```

这会启动一个更全面的测试，包括：
- 创建多个并发连接
- 发送大量消息
- 监控性能指标
- 验证系统在负载下的稳定性

#### 自定义压力测试参数

可以通过命令行参数自定义测试配置：

```bash
go test -v -run TestStressE2E example/e2e_test/e2e_test.go -conns=200 -msgs=5000 -size=4096 -duration=2m -count=1
```

参数说明：
- `-conns=200`: 设置 200 个并发连接
- `-msgs=5000`: 每个连接发送 5000 条消息
- `-size=4096`: 每条消息大小为 4KB
- `-duration=2m`: 测试持续 2 分钟
- `-count=1`: 确保不使用缓存的测试结果

#### 查看测试结果

测试会输出详细的统计信息，包括：
- 连接成功/失败数
- 消息发送/接收数量
- 数据吞吐量（消息/秒和 MB/秒）
- 平均消息延迟
- 服务器和客户端的性能指标

示例输出：
```
=== 30.0s ===
CLIENT: Conns: 100/100 (act/tot), Success: 100, Failed: 0, Msgs: 50000→49850, Rate: 1650.5→1642.0 msg/s, BW: 3.45 MB/s, Errors: 5, Latency: 12.34 ms
SERVER: Conns: 100/100 (act/tot), Msgs: 49850, Rate: 1642.0 msg/s, BW: 3.45 MB/s
```

## 极限测试场景

对于验证系统的极限性能和稳定性，可以设置更高的负载参数：

```bash
go test -v -run TestStressE2E example/e2e_test/e2e_test.go -conns=500 -msgs=10000 -size=8192 -duration=10m -count=1
```

这将模拟极端条件下的使用场景，帮助发现潜在的性能瓶颈或稳定性问题。

## 问题排查

如果遇到测试问题：

1. 检查端口是否被占用
   ```bash
   lsof -i :8000 -i :1080 -i :8080
   ```

2. 确保没有防火墙拦截
   ```bash
   sudo ufw status # Linux
   # 或
   sudo pfctl -s all # macOS
   ```

3. 检查系统资源限制
   ```bash
   ulimit -a
   ```

4. 增加日志详细程度
   ```bash
   ./goflyway -v -v -k testkey -w :8100
   ```

## 贡献

欢迎提交问题和拉取请求来帮助改进 goflyway！
