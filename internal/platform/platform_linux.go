//go:build linux

package platform

// ExeSuffix is appended to helper executable names.
const ExeSuffix = ""

// UnsupportedNote is shown wherever a Windows-only native feature is requested.
// On Linux, ADI headers / six-digit codes and the account-private Mac APS
// bootstrap use the pinned OpenBubbles Linux module; push uses the Go client.
const UnsupportedNote = "Linux 版使用 OpenBubbles 官方 Linux 模块生成认证头和六位码，推送走 Go 客户端；iCloud for Windows 推送不适用。如果提示缺少模块，请按 README-linux.md 下载并校验 librust_lib_bluebubbles.so"

func current() Capabilities {
	return Capabilities{OS: Name, NativeADI: true, NativePush: false, MacPushBootstrap: true, Note: ""}
}
