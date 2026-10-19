# 🌐 BabelSub (巴别字幕)

> **打破巴别塔的语言隔阂 · 专为外语沉浸式学习打造的高性能媒体库双语字幕系统**  
> 支持 **Emby / Jellyfin / Plex 与 Infuse**，采用 **Go + Python Sidecar (ffsubsync) + 零扫盘 REST 架构**，彻底杜绝网盘卡顿与 115 API 风控。

---

> [!WARNING]
> ### ⚠️ 学习者特别提示与免责声明 (Learner's Notice & Disclaimer)
> 1. 本项目专为**外语影视沉浸式学习（Language Immersion & Acquisition）**打造，核心目标是提供优质的双语伴读对照。
> 2. 部分双语字幕基于算法自动对齐、公开网络字幕源抓取或机器/大模型辅助生成，**可能存在局部生僻俚语误译、语境偏差或毫秒级时间戳细微差异**。
> 3. 请将本字幕作为**听力跟读、语感积累与理解辅助**的工具，而非绝对权威词典。若在观影学习中发现翻译瑕疵，欢迎随手提 PR / Issue 贡献精校字幕！

---

## 🌟 核心特性

- **为语言习得而生（Language Immersion First）**：
  - 自动识别影视原生音轨语言（如日语、英语、法语、韩语），精准定位并制备双语字幕。
  - 严格规范 1080p 标准蓝光画布（`PlayResX: 1920 / PlayResY: 1080`），原语种大字清晰突出、母语辅助字优雅微缩，彻底告别字幕爆框遮挡。
- **零扫盘架构（Zero Disk Scan）**：
  - 绝对不触碰 CloudDrive2 / 115 / Alist 挂载目录的文件树递归扫描，全部通过媒体库 REST API 毫秒级提取，完全免除网盘高频封控风险。
- **毫秒级自适应对轴（Auto Alignment）**：
  - 基于 `ffsubsync` 管道，以原声字幕或低码率音频切片作为基准对齐翻译字幕，彻底根治播放器时间轴漂移。
- **即时回写，Infuse 秒播**：
  - 生成标准 `.zh-ja.ass` / `.zh-en.ass` 并保存于影视同级目录后，自动触发单个条目刷新，Apple TV / iPhone 上的 Infuse 播放器点开即选即播。
- **多公开字幕源无 Key 嗅探**：
  - 自动聚合 SubtitleCat 等多语言字幕源，无需额外注册付费 API Key。

---

## 🛠️ 环境依赖

| 组件 | 版本要求 | 用途说明 |
| :--- | :--- | :--- |
| **Go** | >= 1.21 | 主服务二进制编译、REST API、Web 控制台与 ASS 样式引擎 |
| **ffmpeg** | >= 4.4 | 音视频流元数据嗅探、音频参考切片抽取 |
| **Python** | >= 3.8 | 运行 `ffsubsync` 独立对轴环境（推荐隔离 venv） |
| **ffsubsync** | >= 0.5.1 | 核心语音/字幕对轴算法库 |

---

## 🚀 快速安装与使用

### 1. 一键安装脚本
在项目根目录下执行安装脚本，自动检测 `ffmpeg`、配置隔离 Python 虚拟环境并编译 Go 可执行程序：
```bash
./scripts/install.sh
```

### 2. 手动构建
```bash
# 配置独立 Python 虚拟环境
python3 -m venv ~/.config/babelsub/venv
~/.config/babelsub/venv/bin/pip install ffsubsync

# 编译 Go 核心
go mod tidy
go build -o bin/babelsub ./cmd/babelsub
```

---

## 📖 CLI 命令行操作

### 1. 媒体库连接配置（仅需一次）
```bash
# 连接 Emby 服务
./bin/babelsub config --type emby --host http://192.168.1.100:8096 --token <YOUR_EMBY_API_KEY>

# 连接 Plex 服务
./bin/babelsub config --type plex --host http://192.168.1.81:32400 --token <X-Plex-Token>
```
配置文件将持久化写入 `~/.config/babelsub/config.yaml`。

### 2. 检索电影库条目（零扫盘）
```bash
# 检索库内所有原声为日语的电影条目
./bin/babelsub scan --lang ja

# 仅显示尚未就绪双语字幕的电影
./bin/babelsub scan --lang ja --missing-only
```

### 3. 公开源搜索下载字幕
```bash
# 检索指定电影的可用原声/多语言字幕
./bin/babelsub fetch --query "Swing Girls"
```

### 4. 手动对轴并导出标准双语 ASS
```bash
# 以日文字幕 ref.srt 为参考基准，对齐中文字幕 in.srt，输出为标准学习向双语 ASS
./bin/babelsub merge --ref "path/to/ja.srt" --in "path/to/zh.srt" --out "path/to/output.zh-ja.ass"
```

---

## 🖥️ Web 管理界面

启动内置控制台：
```bash
./bin/babelsub --port 18980
```
在浏览器打开 **http://localhost:18980**，即可在可视化界面中浏览影视条目、过滤语种、查看双语就绪状态并触发媒体库挂载刷新。

---

## 📂 项目结构

```text
BabelSub/
├── bin/                 # 编译产物目录 (babelsub 单文件)
├── cmd/
│   └── babelsub/        # CLI 及 Web GUI 主入口 (main.go)
├── pkg/
│   ├── adapter/         # Emby / Jellyfin / Plex 统一零扫盘适配层
│   ├── ass/             # 纯 Go ASS 样式引擎（带 1080p 画布约束与学习排版）
│   ├── config/          # YAML / JSON 双格式配置管理器
│   ├── downloader/      # 多源公共字幕嗅探与下载器
│   └── syncer/          # ffsubsync 流式进程管道封装
├── web/                 # Web 控制台内嵌 HTML/JS 前端资源
├── scripts/
│   └── install.sh       # 一键依赖安装与构建脚本
├── docs/
│   └── ARCHITECTURE.md  # 架构设计约束与开发指南
├── go.mod
└── README.md
```

---

## 📄 开源协议

本项目基于 [MIT License](LICENSE) 开源发布。
