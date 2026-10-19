# BabelSub

BabelSub 是一个专为语言学习（如看原声日影、美剧学外语）设计的双语字幕处理工具。

写这个工具的初衷是因为自己在 Infuse / Emby 看外语片时，经常找不到舒服的双语字幕：
- 播放器内置刮削下的外挂字幕经常和原盘帧率对不上，时间轴越播越偏；
- 网盘（如 115 / CloudDrive2）直接全盘扫描极易触发限流风控；
- 学习外语时需要原声语言在上、中文在下，但很多现成双语字幕排版字体要么巨大遮挡屏幕，要么比例不合适。

BabelSub 通过对接 Emby / Jellyfin / Plex 的 API 快速读取库内影片信息（无需扫盘），自动获取或导入现有单语字幕，利用 `ffsubsync` 以前期原声字幕（或音频）为基准将中文字幕对齐，最后拼合成标准排版的双语 ASS 字幕写回目录。

> **关于字幕内容的说明**：  
> 本工具仅负责字幕时间轴的对齐、缝合与排版输出，字幕文本内容均来自公开网络源或原盘自带轨。字幕本身的翻译质量取决于原作者或抓取源，工具本身不做翻译背书，仅供语言学习参考。

---

## 功能特性

- **零扫盘**：仅通过 Emby/Jellyfin/Plex API 读取电影信息，不遍历挂载盘文件树，避免 115 / 网盘限流。
- **自动对轴**：基于 `ffsubsync`，用原语言字幕作为黄金参考基准，自动修正中文字幕的偏移和帧率拉伸。
- **正常排版**：固定 1080p 画布基准（`PlayResX: 1920` / `PlayResY: 1080`），原语言大字、中文微缩居下，适配 Infuse 等播放器。
- **即时刷新**：生成 `.zh-ja.ass` / `.zh-en.ass` 写回电影目录后，调用 API 通知媒体库局部刷新，Infuse 打开即可选择。
- **自带 Web 界面**：除了命令行，也支持在浏览器里直接点选处理。

---

## 依赖要求

- **Go** (>= 1.21)
- **ffmpeg** (用于音频流或媒体信息探测)
- **Python 3** + **ffsubsync** (对轴核心)

---

## 安装

### 方式一：一键脚本
```bash
./scripts/install.sh
```

### 方式二：手动构建
```bash
# 1. 准备 Python 虚拟环境并安装 ffsubsync
python3 -m venv ~/.config/babelsub/venv
~/.config/babelsub/venv/bin/pip install ffsubsync

# 2. 编译二进制
go mod tidy
go build -o bin/babelsub ./cmd/babelsub
```

---

## 使用指南

### 1. 配置服务器（初次使用）
配置你的媒体服务器连接，信息会保存在 `~/.config/babelsub/config.yaml`：
```bash
# Emby 示例
./bin/babelsub config --type emby --host http://192.168.1.100:8096 --token YOUR_API_KEY

# Plex 示例
./bin/babelsub config --type plex --host http://192.168.1.81:32400 --token YOUR_PLEX_TOKEN
```

### 2. 扫描库内电影
```bash
# 查看所有日语原声电影
./bin/babelsub scan --lang ja

# 仅列出尚未制作双语字幕的电影
./bin/babelsub scan --lang ja --missing-only
```

### 3. 检索公开字幕
```bash
./bin/babelsub fetch --query "Swing Girls"
```

### 4. 对轴并合并双语字幕
拿一份参考字幕（通常是原语言字幕）和一份待对轴字幕（如中文），自动校准并导出 ASS：
```bash
./bin/babelsub merge \
  --ref "/path/to/japanese_ref.srt" \
  --in "/path/to/chinese_raw.srt" \
  --out "/path/to/output.zh-ja.ass"
```

### 5. Web 控制台
```bash
./bin/babelsub --port 18980
```
启动后在浏览器打开 `http://localhost:18980`，可查看电影列表及双语状态。

---

## 配置文件示例

文件路径：`~/.config/babelsub/config.yaml`

```yaml
server:
  type: emby                    # emby / jellyfin / plex
  host: http://192.168.1.100:8096
  token: your_api_key_here

styling:
  play_res_x: 1920              # 1080p 画布宽度
  play_res_y: 1080              # 1080p 画布高度
  font_name: Arial
  primary_font_size: 44         # 上层主语言字号
  secondary_font_size: 30       # 下层中文辅助字号
  primary_color: "&H00FFFFFF"   # 白色
  secondary_color: "&H00D0FFFF" # 淡金/淡黄
  margin_v: 35                  # 底部边距

language:
  target_audio: ja              # 默认筛选的原声音轨
  native_sub: zh                # 默认母语字幕语言

ffsubsync_bin: ffsubsync        # 可指定绝对路径，如 ~/.config/babelsub/venv/bin/ffsubsync
```

---

## License

[MIT](LICENSE)
