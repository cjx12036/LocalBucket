# LocalBucket

局域网文件共享 App：每台运行软件的电脑提供自己的 bucket，在 App 内浏览所有在线节点并存取文件。

**当前仅为可丢弃的拖拽验证原型，不是完整产品。**

## 下载

打开 [Releases](https://github.com/cjx12036/LocalBucket/releases)，在 Assets 中下载：

| 文件 | 用途 |
| --- | --- |
| `LocalBucketDragSpike-win-x64.exe` | Windows 10 x64，双击运行；内置 Dufs 和所需 VC++ 运行库 |
| `LocalBucketDragSpike-windows-test.zip` | Windows 程序、测试素材、使用说明和第三方声明 |
| `LocalBucketDragSpike-mac-arm64.zip` | Apple Silicon Mac，解压后打开 App |
| `SHA256.json` | 下载文件校验值 |

不需要下载 GitHub 自动生成的 Source code ZIP 来运行软件。测试程序未做发行证书签名。

## 验证范围

- Mac M4 / macOS 15.7.7：用户已手工确认拖入、拖出可用。
- Windows：已交叉编译和检查打包内容，等待 Win10 实机验证。
- 两台设备之间的远程拖入／拖出：等待联测，尚未判定通过。
- 拖出仅复制／下载，bucket 原文件保留。

原型默认使用 TCP 47931，只有一个专用测试 bucket，没有自动设备发现或正式产品权限管理。请只放测试文件。

操作步骤、缓存位置、构建方式和已知限制见 [拖拽原型说明](spikes/drag-drop/README.md)。

## 产品方向

- Mac 和 Windows 独立 App，无需浏览器入口。
- 软件创建专用空 bucket。
- 局域网免访问码浏览、上传、下载；删除仅在 bucket 所属电脑操作。
- 在一个 App 中查看所有在线节点。
- 文件与文件夹拖入上传，拖出复制／下载。
- 优先轻量分发；最终 UI 框架待两端拖拽验证后确定。

Dufs 及 Microsoft 运行库的第三方声明见 `spikes/drag-drop/vendor/`。仓库不包含用户实际 bucket 数据或诊断日志。
