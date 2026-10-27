# LocalBucket 拖入／拖出验证原型

这是可丢弃的系统能力验证，不是正式 App，也未确定产品框架。

## 当前验证状态

| 项目 | 状态 | 证据／限制 |
| --- | --- | --- |
| Mac M4 / macOS 15.7.7 拖入、拖出 | 用户手工确认通过 | 2026-10-27 会话确认；不推断已经完成跨机测试 |
| Mac 通过 HTTP 读取 Dufs 文件列表 | 本机执行通过 | 原型显示远端列表，Dufs 返回目录 JSON |
| Mac 远端文件放下后下载 | 待两台电脑联测确认 | 已实现 NSFilePromiseProvider；自动化的一次落点未被接收，不记为通过 |
| Win10 x64 原型 | 交叉编译通过，待用户实机测试 | 独立 GUI EXE，嵌入 Dufs；没有在 Mac 上假装执行 Windows 测试 |
| Windows 远端文件拖出 | 待用户实机测试 | IDataObject/CF_HDROP；Shell 请求数据时同步下载到缓存，然后由 Explorer 复制 |

## Windows 测试

1. 将 `dist/LocalBucketDragSpike-win-x64.exe` 复制到 Win10 电脑，普通双击运行。不要以管理员身份运行，否则可能和普通资源管理器产生不同权限级别的拖放限制。
2. 保持 Local 模式：从资源管理器拖一个测试文件到列表空白处，再拖一个测试文件夹进去。
3. 从列表中将文件／文件夹拖到一个新的输出文件夹，检查内容，确认原 bucket 文件仍存在。
4. 点击 `Open test bucket` 可以直接查看专用测试数据目录。

程序会在 `%LOCALAPPDATA%\LocalBucketDragSpike` 下创建：

- `bucket`：专用测试文件，不是用户已有目录。
- `cache`：远端拖出文件的下载缓存，测试期间不会提前清理。
- `dufs-v0.47.0.exe`：随 EXE 内置、自动释放的文件服务。
- `vcruntime140.dll`：从 Microsoft 官方 VC++ redistributable 提取的 x64 运行库，随 EXE 内置，无需单独安装。
- `events.log`：拖放与下载诊断记录。

关闭原型窗口会退出原型及其启动的 Dufs。不要同时启动多个实例。测试包未做发行证书签名，若系统拦截，请告知具体提示；运行成功与否都记录实际结果。

## Mac / Windows 联测

两台电脑连接到允许设备互访的同一个局域网，分别运行原型。使用各自当前局域网 IPv4 地址，不使用 VPN／虚拟网卡地址。若系统询问是否允许局域网连接，请按实际测试网络授权。

1. 在 Windows 地址框输入 `http://MAC局域网IP:47931/`，点击 `Load remote`。
2. 从资源管理器拖一个**名称尚不存在**的小文本文件到远端列表中；检查 Mac 的测试 bucket 出现同内容文件，Windows 原文件保留。
3. 将 Windows 列表中一个 REMOTE 文件拖到资源管理器输出文件夹；检查下载内容和 Mac 原文件。
4. 在 Mac 原型输入 `http://WINDOWS局域网IP:47931/`，点击 `Load remote`，同样验证两个方向。
5. 记录拖拽是否一次成功、有无明显卡顿、失败时原型提示及 `events.log`。

`http://127.0.0.1:47931/` 可用于单机 HTTP 模式预检，但不能替代跨机测试。

## 范围与限制

- 仅验证原生拖放、真实文件复制和 Dufs HTTP 上传／下载。没有自动发现、托盘、产品权限管理。
- 本机支持文件和文件夹复制。远端模式仅列出和上传普通文件；远端文件夹、多选拖出、大文件进度、取消、磁盘满、网络断开还未验证。
- 强制只向系统提供 COPY 语义，原型不删除 bucket 中的源文件。
- Windows 远端拖出采用同步缓存方案，网络慢时可能卡顿；Shell 可能在松手前就请求文件。不能当作已验证的异步文件承诺方案。
- Mac 使用文件承诺在目标接收后下载。上述 Windows／Mac 原型展示系统能力，不证明某个跨平台 UI 框架已经接通同等行为。
- 端口为 TCP 47931，服务绑定局域网可达地址，仅共享专用测试 bucket，HTTP 无加密、无口令、无远程删除权限。只放合成测试文件。
- Dufs 固定 v0.47.0。保留 `vendor/LICENSE-MIT` 和 `vendor/LICENSE-APACHE`。

## 构建

运行 `python3 build.py`。构建机器需有 Python 3、Go 和 Mac 的 Swift 命令行工具；使用者无需安装这些工具。脚本从 Dufs 官方 GitHub release 获取固定版本，生成 Mac App、Windows EXE 和压缩测试包。

## 官方技术依据

- Apple 文件承诺：https://developer.apple.com/documentation/appkit/supporting-table-view-drag-and-drop-through-file-promises
- Microsoft 文件拖放：https://learn.microsoft.com/en-us/windows/win32/shell/datascenarios
- Dufs：https://github.com/sigoden/dufs/releases/tag/v0.47.0
