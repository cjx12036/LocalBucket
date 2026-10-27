# LocalBucket v0.1

Mac 与 Windows 运行软件后，各自提供一个专用 bucket。打开首页即可自动寻找附近电脑，双击电脑名称后浏览它共享的文件；无需输入地址。

## 使用

1. Mac 解压 `LocalBucket-mac-arm64.zip` 后打开 `LocalBucket.app`；Windows 下载 `LocalBucket-win-x64.exe` 普通双击运行，不需要管理员权限。
2. 允许系统所需的局域网访问。两台电脑都运行 App，并连接到允许设备互访的同一个网络。
3. 首页双击“我的电脑”或另一台电脑。拖入文件／文件夹或点击“添加文件…”存放文件。
4. 选中文件拖到 Finder／资源管理器输出文件夹，或点击“另存到…”。原 bucket 文件保留。
5. 本机 bucket 可以删除到系统回收站；远端只能浏览、上传和下载。
6. 关闭窗口会停止共享并退出。bucket 文件仍然保留，下次打开恢复共享。

同名同内容文件自动跳过，不同内容自动保留两份。App 自己的文件拖回当前目录不复制。

Windows 远端文件第一次拖出时需要先准备下载缓存；界面提示准备完成后，再拖一次即可。可直接使用“另存到…”完成单次下载。Mac 远端目录拖出为 ZIP，普通文件按原文件名复制。

## 数据

- Mac：`~/Library/Application Support/LocalBucket/`
- Windows：程序使用的当前用户应用数据目录 `LocalBucket`，点击“打开本机存储位置”定位 bucket。
- bucket、缓存、配置分开保存；更新 EXE 不会清空文件。
- 如发现旧原型 bucket，首次运行会询问是否复制导入；不会移动或删除旧文件。

本软件在局域网免口令共享 bucket，请仅放希望该网络内其他人访问的文件。来宾 WiFi／设备隔离、VPN、多网卡或系统防火墙可能影响发现，可从帮助查看网络诊断。

## 构建与签名

在装有 Go 1.26+、Swift 命令行工具和 Python 3 的 Mac 上运行 `python3 app/build.py`，生成两平台产物。构建依赖不需要最终使用者安装。

`LOCALBUCKET_WINDOWS_SIGN_COMMAND` 可配置签名工具与参数，构建会依次签内部 Dufs 和最终 EXE。该命令最后会追加待签文件路径，须包含可信签名证书／服务和时间戳参数；不要把凭证提交到仓库。`LOCALBUCKET_MAC_SIGN_IDENTITY` 可指定 Mac 签名身份；默认仅本地 ad-hoc 签名，不包含开发者公证。

未配置受信任 Windows 签名时，产物仍是未签名测试版，SmartScreen 可能提示。新文件即使有有效签名也不能保证立即无提示，参见 [微软说明](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation)。程序不关闭或绕过系统保护。

自动测试：`cd app && go test -race ./internal/node`。设置 `LOCALBUCKET_TEST_DUFS` 为 Mac Dufs 路径可运行真实文件服务下载／退出集成测试。Windows 编译成功不代表已经在 Win10 运行验证。
