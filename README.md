# LocalBucket

局域网文件共享 App：每台运行软件的电脑提供自己的 bucket，在 App 内浏览所有在线节点并存取文件。

**当前版本为 v0.1 功能预览版。** 原生 App 包含自动发现、电脑名称首页、文件夹浏览、拖拽存取和防重复规则；此前拖拽原型保留在 `spikes/`。

## 下载

打开 [Releases](https://github.com/cjx12036/LocalBucket/releases)，在 Assets 中下载：

| 文件 | 用途 |
| --- | --- |
| `LocalBucket-win-x64.exe` | Windows 10 x64，双击运行；内置 Dufs 和所需 VC++ 运行库 |
| `LocalBucket-windows.zip` | Windows 程序、使用说明和第三方声明 |
| `LocalBucket-mac-arm64.zip` | Apple Silicon Mac，解压后打开 App |
| `SHA256.json` | 下载文件校验值 |

不需要下载 GitHub 自动生成的 Source code ZIP 来运行软件。测试程序未做发行证书签名。

## 使用

打开 App，自动显示在线电脑名称；双击电脑进入其 bucket，拖入存放，拖出复制／下载。相同内容跳过，同名不同内容自动保留两份。删除仅限本机并进入系统回收站。关闭窗口停止共享并退出。

程序使用 TCP/UDP 47931 和 mDNS 自动发现，无需输入地址。每台电脑必须运行 App，网络需允许设备互访。局域网访问免口令，请只放希望网络内其他人访问的文件。

操作步骤、数据位置、构建方式、签名配置及已知限制见 [App 说明](app/README.md)。旧验证原型见 [原型说明](spikes/drag-drop/README.md)。

## 产品方向

- Mac 和 Windows 独立 App，无需浏览器入口。
- 软件创建专用空 bucket。
- 局域网免访问码浏览、上传、下载；删除仅在 bucket 所属电脑操作。
- 在一个 App 中查看所有在线节点。
- 文件与文件夹拖入上传，拖出复制／下载。
- Mac 使用 AppKit，Windows 使用 Win32/OLE，共用 Go 节点服务；Dufs 仅作为内部文件服务。

## 验证和发行边界

共享规则、权限隔离、两节点传输及退出有自动测试。Mac 本机执行过原生界面检查；新 Windows UI 仍需 Win10 实机拖放回归。Windows 远端文件首次拖出要先准备缓存，完成后再拖一次，也可用“另存到…”。

默认构建未配置受信任 Windows 签名，SmartScreen 仍可能提示；代码不会关闭系统保护，签名也不能保证新程序立即免提示。Mac 默认仅本地 ad-hoc 签名，公开发行公证需单独配置。

Dufs 及 Microsoft 运行库的第三方声明见 `spikes/drag-drop/vendor/`。仓库不包含用户实际 bucket 数据或诊断日志。
