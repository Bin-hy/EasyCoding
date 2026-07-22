package permission

import "regexp"

// blacklist 内置危险命令正则集（启发式、非完备、不可配置放开）。
//
// 覆盖已知高危模式：递归强删根/家目录、写块设备、fork 炸弹、
// 重定向覆盖磁盘设备、格式化文件系统、递归改权限到 777 等。
// 用户不可增删或关闭黑名单；bypassPermissions 也拦。
var blacklist = []*regexp.Regexp{
	// rm -rf 针对根目录、家目录、所有文件的递归强删
	regexp.MustCompile(`rm\s+(-[a-zA-Z]*[rf][a-zA-Z]*\s+)+(/|~|\$HOME|\$HOME/|/\*)`),

	// dd 写入块设备
	regexp.MustCompile(`dd\s+.*of=/dev/(sd|hd|nvme|disk|xvd|vd|mmcblk|loop|ram|pmem)`),

	// fork 炸弹（经典 :(){ :|:& };: 模式）
	regexp.MustCompile(`:\(\)\s*\{[^}]*\|[^}]*&\s*\}`),

	// mkfs 系列（mkfs.ext4, mkfs.xfs, mkfs.btrfs, mkfs.fat, mkfs.ntfs 等）
	regexp.MustCompile(`\bmkfs\.`),

	// 重定向覆盖 /dev 块设备
	regexp.MustCompile(`>\s*/dev/(sd|hd|nvme|disk|xvd|vd|mmcblk|loop|ram|pmem)`),

	// chmod -R 777 针对 / 或 /etc 等敏感目录
	regexp.MustCompile(`chmod\s+-R\s+0?777\s+(/|/etc|/bin|/sbin|/usr|/var)`),

	// > /dev/sda 写入原始块设备（含 dd-style）
	regexp.MustCompile(`\bdd\s+if=.*\s+of=/dev/(sd|hd|nvme)`),

	// 递归删除根目录变体（不带 -r 但 --no-preserve-root）
	regexp.MustCompile(`rm\s+.*--no-preserve-root\s+(/|/\*)`),

	// mv 覆盖关键系统文件
	regexp.MustCompile(`\bmv\s+.*\s+(/etc/passwd|/etc/shadow|/etc/sudoers|/boot/)`),

	// 清除整个磁盘的分区表
	regexp.MustCompile(`(wipefs|dd)\s+.*/dev/(sd[a-z]|hd[a-z]|nvme\dn\d)\b`),
}

// hitsBlacklist 检查命令串是否命中任一危险模式。
func hitsBlacklist(command string) bool {
	for _, re := range blacklist {
		if re.MatchString(command) {
			return true
		}
	}
	return false
}
