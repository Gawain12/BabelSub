# 架构设计与二次开发指南 (Architecture & AI Development Guide)

本文档旨在为后续维护者或 AI 助手（如 Claude / GPT-4 / Cursor / Cline）提供核心设计约束与代码扩展指南。

---

## 1. 核心约束与红线规则 (CRITICAL CONSTRAINTS)

在对此项目进行任何修改或功能扩展时，必须严格遵守以下原则：

1. **绝对禁止全盘递归扫描 (Zero Mass Filesystem Walk)**：
   - 目标场景中，媒体库的底层文件常部署于 **115网盘 / CloudDrive2 / Alist**。
   - **严禁** 执行 `filepath.Walk`、`os.Walk` 对媒体根目录做深度遍历，否则会触发 115 API 的并发/高频风控，导致挂载卡死甚至封禁。
   - 所有库内检索必须经由 `adapter.MediaServerAdapter`（利用 Emby / Jellyfin / Plex 数据库或内存索引）完成。

2. **ASS 样式画布基准 (Resolution Baseline)**：
   - 在 `pkg/ass/ass.go` 中，导出的 ASS 文件必须显式带有 `PlayResX: 1920` 和 `PlayResY: 1080`。
   - 若缺失此项，不同播放器（特别是 iOS/Apple TV 上的 Infuse）会按照标清基准强行放大字体，导致字幕遮挡整个屏幕。

3. **零 CGO 编译依赖 (Keep Pure Go & Sidecar)**：
   - 保持 Go 主工程的独立编译能力，不要引入重型 C 依赖（如 cgo-ffmpeg），以确保可以交叉编译出直接发布的跨平台单文件。
   - 对轴算法 `ffsubsync` 统一通过 `pkg/syncer/syncer.go` 以独立子进程执行。

---

## 2. 核心模块与扩展点 (Extension Points)

### 2.1 增加新的媒体库后端 (`pkg/adapter`)
实现 `adapter.go` 中的 `MediaServerAdapter` 接口即可：
```go
type MediaServerAdapter interface {
    Ping() error
    ListMovies(targetAudioLang string) ([]MediaItem, error)
    RefreshItem(itemID string) error
}
```

### 2.2 增加新的字幕下载源 (`pkg/downloader`)
在 `downloader/provider.go` 中扩展相应站点的检索抓取方法，无需依赖需要付费的私有 API，优先利用：
- 公开搜索 HTML 页面解析；
- 开放的公共 REST API（如 ASSRT、OpenSubtitles REST 等）。

### 2.3 音频切片兜底对轴 (Audio Fallback)
若某一电影在互联网上完全无法找到原语种字幕，可通过 `ffmpeg` 切取原片前 10~15 分钟的极轻量音频流（16kHz 单声道 mp3，仅需约 2~3MB 数据传输），传入 `ffsubsync` 的参考源参数：
```bash
ffmpeg -ss 120 -t 600 -i <StreamURL> -ac 1 -ar 16000 -b:a 32k /tmp/ref_audio.mp3
ffsubsync /tmp/ref_audio.mp3 -i target.srt -o output.srt
```

---

## 3. 开发测试验证流

任何代码改动后，在终端执行以下命令进行端到端回归：
```bash
# 1. 单元编译检查
go test ./...
go build -o bin/subsync ./cmd/subsync

# 2. 验证服务器连通与读取
./bin/subsync scan --lang ja

# 3. 验证字幕抓取
./bin/subsync fetch --query "Swing Girls"
```
