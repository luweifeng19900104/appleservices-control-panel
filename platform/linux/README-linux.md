# appleservices · Linux 版

## 现在能用什么

| 功能 | Linux |
|---|---|
| 网页面板、账号列表、添加/移除账号、已保存的家庭/储存/方案缓存查看 | 可用 |
| 内存守护（预留内存、本程序上限、排队/暂停/自动恢复） | 可用（/proc/meminfo、cgroup v2/v1、/proc/<pid>/status） |
| 本机 ADI 认证头与六位验证码（`-native-adi`，默认开启） | 可用：OpenBubbles 官方 Linux 模块 v1.15.0+205（`librust_lib_bluebubbles.so`），用 ctypes 加载，函数号与 Windows 版相同 |
| 账号独立 Mac 推送身份初始化（`tools/mac-push-check.py`） | 可用（同一模块，写出 `push.json`） |
| 推送接收 | 走 Go 客户端 `aps/client.go`；ADI 辅助进程里模块自带的 5223 连接被 `LD_PRELOAD` connect 垫片拒绝（只作用于该进程） |
| iCloud for Windows 推送（`-native-push`） | 不适用 |

运行依赖：`python3`（≥3.10，64 位）、`python3-cryptography`、`gcc`（启动时编译 5223 垫片）、`curl`。
本机模块受 OpenBubbles 许可约束，**不随本项目分发**：由 `setup-native.sh` 从官方 GitHub 发布页下载并按 sha256 校验
（tar `5584a4e7…563a`，so `ce260415…10cf`）。

## 目录（全部在数据盘 /data）

| 内容 | 位置 |
|---|---|
| 程序 | `/data/appleservices/releases/<版本>`，`/data/appleservices/current` 指向当前版本（旧版本保留，可回退） |
| 本机模块 | `/data/appleservices/native/librust_lib_bluebubbles.so`（每个版本目录里的 `native` 是指向它的链接） |
| 状态 | `/data/appleservices/state`（0700，属于系统用户 `appleservices`） |
| 日志 | `/data/appleservices/logs/dashboard.log`；每个账号后台日志在 `state/accounts/<id>/daemon.*.log` |
| 临时文件 | `/data/appleservices/tmp`（服务的 TMPDIR） |
| 配置 | `/etc/appleservices/appleservices.env`（只有配置；不要写账号密码） |
| 可选链接 | `/opt/appleservices -> /data/appleservices`（仅当 `/opt/appleservices` 不存在时创建） |

换数据目录：`sudo APPLESERVICES_ROOT=/其他/路径 ./install.sh`。

## 安装（systemd）

```sh
tar xzf appleservices-linux-dist.tar.gz
cd linux
sudo ./install.sh            # 下载并校验本机模块、安装并启动；--no-start 只安装；--uninstall 卸载（保留状态、日志、模块）
```

只监听 `127.0.0.1`；服务以非 root 用户运行，带 systemd 加固选项（`ProtectSystem=strict`，只允许写 state/logs/tmp）。

常用命令：

```sh
sudo systemctl status appleservices
tail -f /data/appleservices/logs/dashboard.log
sudo systemctl restart appleservices
```

回退：`sudo ln -sfn /data/appleservices/releases/<旧版本> /data/appleservices/current && sudo systemctl restart appleservices`

## 远程访问（SSH 隧道）

不要把端口开放到公网。在自己的电脑上：

```sh
ssh -N -L 18741:127.0.0.1:18741 <用户>@<服务器>
```

然后浏览器打开 **http://127.0.0.1:18741/**。注意：

- 本地端口必须与服务器端口相同（面板只接受 Host 为 `127.0.0.1:<端口>` 的请求），并且用 `127.0.0.1`，不要用 `localhost`。
- 改端口时修改 `/etc/appleservices/appleservices.env` 的 `APPLESERVICES_PORT`，隧道两端用同一个数字。

Windows 上可用 `ssh`（OpenSSH 客户端自带）或 PuTTY 的 Tunnels 设置同样的转发。

## 公网 IP + HTTPS 端口

面板仍只监听 `127.0.0.1`。公网访问通过 `tunnelgate` 和 Nginx 双层转发，入口随机密钥先换成
`Secure` / `HttpOnly` / `SameSite=Strict` Cookie，之后仍需面板自己的管理密码。不要把 18741 直接开放。

服务器已经有覆盖公网 IP 的 TLS 证书时：

```sh
sudo /data/appleservices/current/enable-public-gateway.sh <公网IPv4> 18742
```

脚本会安装并启用 `appleservices-gateway.service`、新增独立 Nginx 监听并开放本机 UFW 端口，现有
80/443 站点不受影响。运行结果会打印一次完整入口 URL：

```text
https://<公网IPv4>:18742/?k=<随机入口密钥>
```

入口密钥保存在 `/var/lib/appleservices-gateway/gate.key`，重启后保持不变。删除该文件并重启
`appleservices-gateway.service` 可轮换密钥。云平台安全组仍需允许相同 TCP 端口。

## 不用 systemd 直接运行

```sh
./run-ui.sh                       # 状态在 ./state，端口 18741，Ctrl+C 停止
STATE_DIR=/data/as-test/state NATIVE=/data/appleservices/native PORT=18750 ./run-ui.sh
```

## 内存守护

面板“内存与资源”页面：系统总内存/可用、本程序合计（面板 + 全部账号后台进程树的 RSS）、每个账号占用、排队/暂停账号，
以及三个可编辑设置（保存在 `/data/appleservices/state/memory-guard.json`，立即生效）：

- 为其他程序保留：默认 1024 MB
- 本程序内存上限：默认 2048 MB（面板自身的 Go 软上限设为其 25%）
- 检查间隔：默认 5 秒

服务运行在受限 cgroup（例如在 unit 中设置 `MemoryMax=`）时，守护按 cgroup 上限计算可用内存。

## 加密保存重连信息

账号密码和设备码接收链接会使用服务器专用的 32 字节密钥进行 AES-256-GCM 加密。主密钥位于
`/etc/appleservices/credential.key`（`root:appleservices`、0640），每个账号的密文位于账号目录的
`reconnect-credentials.enc`（0600）。账号列表、日志和运行状态不保存或返回明文。

重新连接时可以留空密码和设备码链接，面板会复用已保存密文。连接窗口提供“删除已保存的重连信息”；
从账号列表移除账号时也会删除该账号的密文。请把 `credential.key` 纳入受控的服务器备份，丢失该密钥后
旧密文无法恢复，只能重新输入账号信息。

## 构建

```sh
scripts/build-linux.sh            # 或 make linux；GOARCH=arm64 可构建 ARM 版
```

## 临时 Cloudflare 访问（无法开放入站端口时）

`bin/tunnelgate` 监听一个 unix 套接字，要求一次性随机访问码（`?k=…`，换成 Secure/HttpOnly/SameSite=Strict cookie），
再把 Host/Origin 改回 `127.0.0.1:<端口>` 转发给面板，面板自己的检查不变。配合 cloudflared 快速隧道：

```sh
bin/tunnelgate -socket /data/appleservices/tmp/gate.sock -upstream 127.0.0.1:18741 -key-file /data/appleservices/tmp/gate.key &
cloudflared tunnel --no-autoupdate --url unix:/data/appleservices/tmp/gate.sock
# 打开 https://<随机名>.trycloudflare.com/?k=<gate.key 内容>
```

用完立刻停止 cloudflared 和 tunnelgate。
