# appleservices · Windows 版

Windows 版行为与改造前（`windows-baseline` 标签）一致：面板仍以 `-native-adi` 启动每个账号后台，
使用固定哈希的 OpenBubbles 原生模块、iCloud for Windows 推送和 Mac 推送身份初始化。新增的只有“内存与资源”页面（见下）。

## 目录布局

`dist\windows\` 与原来的 `app\` 文件夹布局相同，可以整体替换 `D:\appleservices-family-private\app\` 中的程序文件：

```
bin\dashboard.exe  bin\login.exe  bin\trusted-device-gui.exe  bin\trusted-device.exe
run-ui.ps1  run-login.ps1  run-multi.ps1  run-trusted-device.ps1
setup-native-adi.ps1  setup-installed-native.ps1  reset-icloud-auth.ps1
tools\mac-push-check.py  nativeadi\*.py  gsa\*.pem …   （运行时需要的源码树文件）
```

不包含：`state\`（账号状态）、`.cache\`（原生模块 DLL）、`.venv\`、`tools\w64devkit\`。替换程序文件前先停止面板，**不要覆盖 `state\` 和 `.cache\`**。

## 启动 / 停止

```powershell
& 'D:\appleservices-family-private\run-ui.ps1'          # 启动并打开 http://127.0.0.1:18741/
& 'D:\appleservices-family-private\run-ui.ps1' -Stop    # 停止面板（账号后台继续保持登录）
```

`run-ui.ps1` 同时支持两种放置方式：放在 `app\` 的上一级（原布局），或放在 app 文件夹内部（`dist\windows` 布局）。
Python `.venv` 和 `tools\w64devkit` 会先在脚本所在目录找，再到 app 文件夹的上一级找。也可以用 `-AppDir <路径>` 指定。

前置条件不变：OpenBubbles 1.18.800.0 x64（Store，`setup-installed-native.ps1` 复制并校验 DLL）、iCloud for Windows、
`.venv`（cryptography、requests）、w64devkit gcc。

## 构建

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build-windows.ps1     # 在 Windows 上
make windows                                                            # 或在 Linux 上交叉编译（CGO 关闭）
```

项目没有 cgo 代码，Windows 版交叉编译与本机编译使用同样的纯 Go 路径。

## 内存与资源（新增）

侧栏“内存与资源”显示系统总内存/可用内存、本程序合计（面板 + 所有账号后台及其子进程的工作集）、每个账号占用、排队/暂停的账号，
并可修改：为其他程序保留（默认 1024 MB）、本程序上限（默认 2048 MB）、检查间隔（默认 5 秒）。设置保存在 `state\memory-guard.json`，立即生效。
