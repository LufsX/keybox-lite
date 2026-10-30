# Keybox Lite

[English](README.en.md)

Keybox Lite 是 KernelSU、APatch 和 Magisk 的模块。它更新 Tricky Store、
TEESimulator、TEESimulator-RS 和 OhMyKeymint 的 keybox。触发更新的方式有两个：
模块的 Action 按钮，或内置 WebUI。

## 前置条件

- Android 设备，arm64 架构
- KernelSU、APatch 或 Magisk
- 系统有 `curl` 或 `wget`

## 安装

1. 下载最新版本的 zip 包。
2. 在 KernelSU、APatch 或 Magisk 中刷入 zip 包。
3. 重启设备。

## 使用

- Action 按钮：点击模块的 Action 按钮，模块开始更新 keybox。KernelSU 和 APatch
  有这个按钮。
- WebUI：打开模块的 WebUI。点击「更新 keybox」。
- 定时更新：在 WebUI 中选择「每天」或「每周」。
- 停止更新：在 WebUI 中点击「停止更新」。

## 支持的后端

| 后端 | 检测方式 | keybox 路径 | 附加要求 |
| --- | --- | --- | --- |
| Tricky Store | 模块 id 为 `tricky_store` | `/data/adb/tricky_store/keybox.xml` | 至少 1 个通过认证的 key |
| Tricky Store 旧版 | 模块 id 为 `tricky_store`，versionCode 较小或缺失 | 同 Tricky Store | 同 Tricky Store |
| TEESimulator-RS | 模块 id 为 `tricky_store`，模块名为 `TEESimulator-RS` 或有 `libcertgen.so` | 同 Tricky Store | 同 Tricky Store |
| OhMyKeymint | 模块 id 为 `oh_my_keymint` | `/data/misc/keystore/omk/keybox.xml` | 必须有 EC key |
| TEESimulator | 模块 id 为 `teesim` | `config.json` 的 `profiles.default.keybox`，默认是 `/data/adb/teesim/keybox.xml` | 必须有 EC key 和 RSA key |

模块按顺序检测已安装的模块：Tricky Store、OhMyKeymint、TEESimulator。
模块不向已停用和待删除的模块安装。
检测到多个后端时，模块只更新选中的那一个。
TEESimulator 只更新 `profiles.default`，不重写各应用的自定义配置。

## 校验与安装

安装前，模块校验候选 keybox 的 4 项内容：结构、密码学、有效期和吊销状态。

- 结构：XML 结构，以及 key 与证书的算法。
- 密码学：私钥运算，key 与证书的对应，证书签名，X.509 约束。
- 有效期：全部证书都在有效期内。模块按设备时间判断。
- 吊销状态：模块向 Google 官方状态列表查询。

候选还必须满足所选后端的附加要求（见上一节）。
4 项校验全部通过后，模块才安装该候选。
吊销状态无法查询时，模块跳过该候选。
如果启用了「跳过吊销检查」，模块不查吊销状态，其余校验照常执行。

安装前，模块备份旧文件。替换是一次原子写入。
模块保留已安装文件的属主和权限。新文件用 0644。

替换前，模块再次确认后端和 keybox 路径。
一个候选失败时，模块继续尝试下一个候选。
安装完成后，模块不重启后端的守护进程。

## WebUI

- 显示已安装的 keybox 路径、大小和证书列表。
- 显示每张证书的生效时间、到期时间和时间有效性。
- 提供「更新 keybox」、「证书检测」和「停止更新」。
- 定时更新支持「关闭」、「每天」和「每周」。
- 供应源支持「自动」、「Meow」和「Spectre」。
- 「目标管理」编辑应用列表、跟踪模式和补丁策略。
- 支持导入本地 keybox 文件，以及恢复已备份的 keybox。
- 支持导出和导入应用配置，格式是 JSON。
- 可选 WebUI 入口：在后端模块中添加本模块的入口。

## 构建

`sh package.sh` 构建 Go 二进制和 WebUI，并生成 zip 包。
构建需要 Go、Node.js、npm 和 zip。
UPX 压缩可选。
输出文件是 `dist/keybox-lite-<version>-<versionCode>.zip`。

## 许可

Copyright (C) 2026 LufsX。

本项目使用 GNU General Public License 第 3 版或更新版本（GPL-3.0-or-later）。
许可证全文见 [LICENSE](LICENSE)。
WebUI 中包含的第三方组件见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

## 署名

- 目标管理的配置格式和自动跟踪行为参照 [Duck ToolBox](https://github.com/eltavine/Duck-ToolBox)（MIT）。
- Tricky Store 目标管理来自 KOWX712 的 [Tricky Addon: Update Target List](https://github.com/KOWX712/Tricky-Addon-Update-Target-List)（Apache-2.0）。
