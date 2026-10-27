# LocalBucket

**两台电脑传文件，还在来回折腾？LocalBucket，让文件拖过去就好。**

[English](README.en.md) · **简体中文** · [下载预览版](https://github.com/cjx12036/LocalBucket/releases)

一个轻量的 Mac / Windows 局域网传文件小工具。两台电脑打开软件，文件就会出现在同一个共享空间里。无需账号、无需输入地址、无需浏览器。

> 当前为 **v0.2 功能预览版**。请从 [Releases](https://github.com/cjx12036/LocalBucket/releases) 下载最新预览版。

## 下载与开始

| 系统 | 下载文件 | 使用方式 |
| --- | --- | --- |
| Windows 10 x64 | `LocalBucket-win-x64.exe` | 下载后双击，无需管理员权限 |
| Apple Silicon Mac（M 系列，macOS 15+） | `LocalBucket-mac-arm64.zip` | 解压后打开 `LocalBucket.app` |

也提供 Windows ZIP、使用说明与 `SHA256.json` 校验值。运行软件不需要下载 Source code。

1. 两台电脑连接同一局域网，打开 LocalBucket，允许所需的本地网络／防火墙访问。
2. **拖入存放**：首页拖入或点击「添加」，文件存到本机专用存储空间，并出现在统一列表里。
3. **拖出拿走**：拖到桌面或文件夹，或选中后「另存到」。始终复制，原文件保留。

「位置」标明文件在哪台电脑。双击文件夹进入；在文件夹内添加，会存到该文件夹所属电脑。同名文件分别显示，同名文件夹不会跨电脑合并。

## 简单的规则

- **统一查看，不自动同步。** 文件保留在原电脑，对方离线时暂时不可取；不会给每台电脑保存完整副本。
- 相同文件再次导入会跳过；同名不同内容自动保留两份。
- 只能删除本机文件，删除后进入系统废纸篓／回收站。
- 关闭窗口即停止共享并退出，已存文件下次打开仍在。
- 局域网访问免口令：网络内能访问服务的设备可以读取、上传文件，请只存放愿意共享的内容。

## 系统安全提示与当前限制

**目前无法保证免除 Windows SmartScreen 或 macOS Gatekeeper 的拦截／提示。程序不会绕过、关闭系统保护。**

- **Windows**：当前 EXE 尚无受信任的发行签名，可能出现“Windows 已保护你的电脑”。只有确认下载来源可信时，才按系统提供的「更多信息 → 仍要运行」继续；若没有该选项，可能受设备策略限制。即使未来签名，新版本也不保证立即免提示。
- **Mac**：当前仅做本地 ad-hoc 签名，尚无 Developer ID 签名及 Apple 公证。若出现“Apple 无法验证 LocalBucket”，确认来源后到「系统设置 → 隐私与安全性 → 仍要打开」。这是 macOS 安全检查，与企业 MDM 监管不同；受管理设备仍可能有额外限制。参见 [Apple 官方说明](https://support.apple.com/zh-cn/102445)。
- Windows 远端文件拖出需先准备，提示完成后再拖一次；也可直接「另存到」。Mac 远端文件夹拖出为 ZIP。
- 来宾 Wi-Fi 的设备隔离、VPN 或防火墙可能影响自动发现。两台电脑都需运行 App；可在「更多」查看网络诊断。

## 开发

原生 AppKit / Win32 + OLE 界面，共用 Go 节点服务；Dufs 作为内部文件服务。自动发现使用 TCP/UDP `47931` 与 mDNS。

```sh
# 测试共享服务
go -C app test -race ./internal/node

# 在 Mac 上打包两平台（从仓库根目录）
python3 app/build.py
```

构建需要 Go 1.26+、Swift 命令行工具和 Python 3。数据位置、构建签名配置见 [App 说明](app/README.md)，验证范围见 [v0.2 验证记录](docs/validation-v0.2.md)。仓库不包含用户文件；第三方声明见 `spikes/drag-drop/vendor/`，旧原型保留在 `spikes/`。
