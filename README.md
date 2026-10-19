# SubSync Studio (双语字幕自动对轴与管理套件)

> **面向 Emby / Jellyfin / Plex 与 Infuse 玩家的高性能中外双语字幕自动对轴制作系统**  
> 采用 **Go + Python Sidecar (ffsubsync) + 零扫盘 REST 架构**，彻底杜绝网盘卡顿与 115 API 风控。

---

## 🌟 核心特性与设计哲学

1. **零文件扫盘（Zero Disk Scan）**：
   - 不对云盘挂载点（CloudDrive2 / 115 / Alist）执行任何递归扫描，全部通过媒体库 REST API 毫秒级查询，保护网盘不被限流。
2. **毫秒级自动对轴**：
   - 基于 `ffsubsync` 算法，支持以原语种字幕或原片低码率音频流切片作为参考轴，自动对齐目标翻译字幕，彻底根治 Infuse 帧率漂移与字幕不同步。
3. **符合人眼直觉的双语 ASS 排版**：
   - 严格规范 1080p 画布分辨率（`PlayResX: 1920 / PlayResY: 1080`），字体大小自然微缩，杜绝大屏播放时字号占满半个屏幕的问题。
4. **即时回写与 Infuse 秒感知**：
   - 生成的标准 `.zh-ja.ass` / `.zh-en.ass` 写入同级电影目录后，自动触发媒体服务器单条目刷新，Apple TV / iOS 上的 Infuse 点开即有双语字幕。
5. **多字幕源自动兜底**：
   - 支持 SubtitleCat、射手网 (ASSRT)、SubHD 等公共源无 Key 免登录检索。

---

## 🛠️ 系统与环境依赖

| 组件 | 版本要求 | 用途说明 |
| :--- | :--- | :--- |
| **Go** | >= 1.21 | 主服务二进制编译、REST API、Web 控制台与 ASS 样式引擎 |
| **ffmpeg** | >= 4.4 | 音视频流元数据嗅探、音频参考流抽取 |
| **Python** | >= 3.8 | 运行 `ffsubsync` 独立对轴环境（推荐隔离 venv） |
| **ffsubsync** | >= 0.5.1 | 核心语音/字幕对轴算法库 |

---

## 🚀 极速安装与编译

### 1. 一键脚本安装（推荐）
在项目根目录运行安装脚本，将自动检测安装 `ffmpeg`、创建隔离 Python 虚拟环境并编译 Go 单文件：
```bash
./scripts/install.sh
```

### 2. 手动编译（开发者）
```bash
# 1. 准备 Python 虚拟环境并安装 ffsubsync
python3 -m venv ~/.config/subsync-studio/venv
~/.config/subsync-studio/venv/bin/pip install ffsubsync

# 2. 编译 Go 核心
go mod tidy
go build -o bin/subsync ./cmd/subsync
```

---

## 📖 CLI 命令行使用指南

### 1. 媒体服务器初始化（仅需一次）
```bash
# 连接 Emby 服务
./bin/subsync config --type emby --host http://192.168.1.100:8096 --token <YOUR_EMBY_API_KEY>

# 连接 Plex 服务
./bin/subsync config --type plex --host http://192.168.1.81:32400 --token <X-Plex-Token>
```
配置文件将持久化写入 `~/.config/subsync-studio/config.yaml`。

### 2. 检索电影库条目（零扫盘）
```bash
# 扫描库内所有原声为日语的电影条目
./bin/subsync scan --lang ja

# 仅显示尚未制作双语字幕的电影
./bin/subsync scan --lang ja --missing-only
```

### 3. 公开源搜索下载字幕
```bash
# 检索指定电影的可用字幕轨
./bin/subsync fetch --query "Swing Girls"
```

### 4. 手动对轴并导出标准双语 ASS
```bash
# 以日文字幕 ref.srt 为参考基准，对齐中文字幕 in.srt，输出为 movie.zh-ja.ass
./bin/subsync merge --ref "path/to/ja.srt" --in "path/to/zh.srt" --out "path/to/output.zh-ja.ass"
```

---

## 🖥️ Web 管理界面

SubSync Studio 内置了开箱即用的前端控制台：
```bash
./bin/subsync --port 18980
```
在浏览器中打开：**http://localhost:18980**  
- 可视化浏览库内影视列表；
- 切换语言过滤、一键刷新条目外部字幕。

---

## ⚙️ 配置文件说明 (`config.yaml`)

配置文件位于 `~/.config/subsync-studio/config.yaml`，结构如下：
```yaml
server:
  type: emby                    # emby / jellyfin / plex
  host: http://192.168.1.100:8096
  token: your_emby_api_key_here

styling:
  play_res_x: 1920              # 1080p 画布宽度
  play_res_y: 1080              # 1080p 画布高度
  font_name: Arial              # 字体族
  primary_font_size: 44         # 主语言正常字号（杜绝溢出）
  secondary_font_size: 30       # 辅语言字号
  primary_color: "&H00FFFFFF"   # 主语言文字纯白
  secondary_color: "&H00D0FFFF" # 辅语言淡金/浅蓝
  margin_v: 35                  # 底部安全边距

providers:
  enable_subtitlecat: true
  enable_assrt: true
  enable_subhd: true

language:
  target_audio: ja              # 默认原声音轨
  native_sub: zh                # 默认母语字幕

ffsubsync_bin: ffsubsync        # 可配置为自定义 Python 虚拟环境下的绝对路径
```

---

## 📂 项目工程结构

```text
subsync-studio/
├── bin/                 # 编译产物目录 (subsync 单文件)
├── cmd/
│   └── subsync/         # CLI 及 Web GUI 主入口 (main.go)
├── pkg/
│   ├── adapter/         # Emby / Jellyfin / Plex 统一接口实现
│   ├── ass/             # 纯 Go 高性能 ASS 样式生成引擎 (标准 1080p 画布)
│   ├── config/          # YAML / JSON 双格式配置管理器
│   ├── downloader/      # 多源公共字幕嗅探与下载器
│   └── syncer/          # ffsubsync 流式进程管道封装
├── web/                 # Web 控制台内嵌 HTML/JS 前端资源
├── scripts/
│   └── install.sh       # 一键依赖安装与构建脚本
├── docs/                # 进阶架构与开发文档
├── go.mod
└── README.md
```
