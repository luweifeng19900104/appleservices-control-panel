//go:build !windows && !linux

package platform

// ExeSuffix is appended to helper executable names.
const ExeSuffix = ""

// UnsupportedNote is shown wherever a Windows-only native feature is requested.
const UnsupportedNote = "Linux 版暂不支持本机 ADI 验证码生成、Mac 推送身份初始化和 iCloud for Windows 推送（迁移方案阶段 A 待完成）；面板、账号管理、已保存状态查看和命令行登录（login）可用"

func current() Capabilities {
	return Capabilities{OS: Name, Note: UnsupportedNote}
}
