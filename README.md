# ServerStatus-goclient

使用Golang写的ServerStatus-Hotaru客户端，在原有基础上增加对Websocket的支持。

## Usage

```bash
Usage of client:
  -dsn string
        Input DSN, format: username:password@host:port
  -h string
        Input the host of the server
  -interval float
        Input the INTERVAL (default 1)
  -p string
        Input the client's password
  -path string
        WebSocket path (default "/")
  -port int
        Input the port of the server (default 35601)
  -protocol string
        Protocol: tcp or websocket (default "tcp")
  -ssl
        Use SSL for websocket connection
  -u string
        Input the client's username
  -verbose
        Enable verbose logging
  -vnstat
        Use vnstat for traffic statistics, linux only
```

## 使用说明（以Linux为例）

```bash
git clone https://github.com/MJJSUN/ServerStatus-goclient.git

cd ServerStatus-goclient

go get github.com/gorilla/websocket

go build -trimpath -o status-client ./cmd/client

chmod +x status-client
```

运行时需传入客户端对应参数。

假设你的服务端地址是`yourip`，客户端用户名`username`，密码`password`，端口号`port`
```bash
# WebSocket (SSL)
./status-client -dsn "wss://username:password@yourip:port/path"
```

```bash
# WebSocket (非SSL)
./status-client -dsn "ws://username:password@yourip:port/path"
```

```bash
# TCP (传统格式)
./status-client -dsn "username:password@yourip:port"
```

即用户名密码以`:`分割，登录信息和服务器信息以`@`分割，地址与端口号以`:`分割。

默认端口号是35601，所以你可以忽略端口号不写，即直接写`username:password@yourip`

你也可以直接使用参数运行

```bash
./status-client -h yourip -u username -p password -port 443 -protocol websocket -ssl -path / -verbose
```

## 或者使用一键脚本

```shell

wget https://raw.githubusercontent.com/MJJSUN/ServerStatus-goclient/master/install.sh

#安装

bash install.sh

#或

bash install.sh -dsn "xxx"

#修改配置

bash install.sh reset_conf #(re)

#卸载

bash install.sh uninstall #(uni)

```
