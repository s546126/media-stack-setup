# danmu-bridge

把 Sonarr / Radarr 接到 [Misaka 弹幕服务器](https://github.com/l429609201/misaka_danmu_server)：新片入库时自动让 Misaka 按 TMDB/TVDB 去抓弹幕，再把弹幕 xml 写到媒体库里每个视频旁边。Jellyfin 只负责播放。

```
新入库：Sonarr/Radarr webhook ──▶ danmu-bridge ──▶ Misaka /api/control/import/auto（按 ID + 季集）
存量：  danmu-bridge backfill ── 从 Sonarr/Radarr API 拉全部文件 ──逐个提交──▶ Misaka
同步：  每 5 分钟从 Misaka 取弹幕 ──▶ /mnt/data/media/library/{tv,movies}/…/<视频名>.xml
```

一个二进制，三个子命令：

| 命令 | 作用 |
| --- | --- |
| `serve`（默认） | 收 webhook，排队逐个提交给 Misaka；同时按 `SYNC_INTERVAL` 定时同步 xml |
| `backfill` | 把 Sonarr/Radarr 里已有的所有文件提交给 Misaka，限速、可中断续跑 |
| `sync` | 只跑一次 xml 同步然后退出 |

## 它具体做什么

**webhook**：收到 `Download` 事件（Sonarr/Radarr 里叫 On File Import / On File Upgrade）后，取出 TMDB/TVDB/IMDb ID、季号和集号，调用 Misaka 的 `POST /api/control/import/auto`。同一季的多集合并成一次请求（如 `episode=1-3,5`）。Misaka 同一个 API Key 同时只允许一个搜索任务，所以导入是串行的：遇到 429 会退避重试，遇到 409（3 小时内提交过同样的任务，或已在库里）当作已完成。提交后会等任务结束，再触发一次同步。Rename / 删除类事件只触发同步。

**backfill**：从 Sonarr `/api/v3/series`、`/episodefile`、`/episode` 和 Radarr `/api/v3/movie` 拉出所有有文件的条目，按“剧集 + 季”或“电影”分组，一个一个提交，每次之间停 `BACKFILL_INTERVAL`。进度记在 `$STATE_DIR/backfill-state.json`，中断后再跑会跳过已完成的；某一季后来多了新集，会重新提交那一季。

**sync**：
1. 从 Sonarr/Radarr API 拿到每个视频的真实路径（不靠猜文件名）。
2. 从 Misaka `/library` 和 `/library/anime/{id}` 建索引：电视剧按 TMDB 或 TVDB ID + 季号匹配，电影按 TMDB 或 IMDb ID 匹配。
3. 找到对应分集（优先用 Misaka 里“精确标记”的数据源），用 `/danmaku/{episodeId}` 取全部弹幕，按 Misaka 自己的 XML 格式（九段 `p` 属性）写成 `<视频名>.xml`。
4. 幂等：Misaka 那边分集的 `fetchedAt` 和弹幕数没变就不请求；内容哈希没变就不写；写的时候先写临时文件再 rename。
5. 视频没了（被删、被改名、升级成别的扩展名）的 xml 会被删掉。只删它自己写过的 xml（记录在 `$STATE_DIR/sync-state.json`）；视频旁边已经有别人写的同名 xml（比如 Jellyfin 弹幕插件下的）它不碰。判断视频是否存在用的是 `lstat`，所以 Decypharr 的软链接哪怕 rclone 挂载暂时掉了，也不会误删。

xml 写在软链接所在目录（媒体库里），不会跟着链接写进 debrid 挂载。

## 准备 Misaka

1. 在 Misaka 网页端「外部控制 → API 密钥」里拿到外部 API 密钥，填到 `MISAKA_API_KEY`。
2. 在「元信息搜索源」里启用 TMDB（以及想用的话 TVDB）。没启用的源，Misaka 会直接返回 400，日志里会写明。
3. 关掉 Jellyfin 的弹幕下载插件，或者至少别让它和这个服务同时往视频旁边写 xml。

## 部署

镜像同时支持 amd64 和 arm64（Oracle ARM 主机直接用）：

```bash
docker buildx build --platform linux/amd64,linux/arm64 -t danmu-bridge:latest .
# 只在本机 ARM 上构建：
docker build -t danmu-bridge:latest .
```

compose 片段（加入 `arr_default` 网络，这样能用容器名访问 sonarr/radarr/misaka；完整的编排见 media-stack-setup 的 compose-danmaku.yml）。不需要 `LIBRARY_ROOT`，也不需要挂 Misaka 的 config 目录：视频路径从 Sonarr/Radarr API 拿，弹幕从 Misaka 接口取。

```yaml
  danmu-bridge:
    image: danmu-bridge:latest
    container_name: danmu-bridge
    restart: unless-stopped
    user: "1000:1000"                 # 和 linuxserver 容器的 PUID/PGID 一致
    environment:
      - MISAKA_URL=http://misaka:7768
      - MISAKA_API_KEY=<Misaka 外部 API 密钥>
      - SONARR_URL=http://sonarr:8989
      - SONARR_API_KEY=<Sonarr API Key>
      - RADARR_URL=http://radarr:7878
      - RADARR_API_KEY=<Radarr API Key>
      - WEBHOOK_SECRET=<随便生成一串>
      - TZ=UTC
    volumes:
      - ./danmu-bridge-data:/data
      - /mnt:/mnt:rslave        # 和 Sonarr/Radarr 看到的路径一致，就不用 PATH_MAP
    networks: [ arr_default ]
    # 不需要对外暴露端口，Sonarr/Radarr 在同一网络里直接访问 danmu-bridge:8090
```

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MISAKA_URL` | 必填 | 例如 `http://misaka:7768` |
| `MISAKA_API_KEY` | 必填 | Misaka 外部控制 API 密钥，走 `X-API-Key` 请求头 |
| `SONARR_URL` / `SONARR_API_KEY` | | 两个一起填；Sonarr 和 Radarr 至少配一个 |
| `RADARR_URL` / `RADARR_API_KEY` | | 同上 |
| `LISTEN_ADDR` | `:8090` | webhook 监听地址 |
| `WEBHOOK_SECRET` | 空 | 强烈建议设置（旧名 `WEBHOOK_TOKEN` 也认）。可以用 `?token=`、Basic Auth 密码或 `X-Webhook-Token` 头传 |
| `TV_ID_SOURCE` | `tmdb` | 电视剧优先用哪个 ID 提交给 Misaka（`tmdb` 或 `tvdb`），缺的时候自动用另一个 |
| `SYNC_INTERVAL` | `5m` | 同步间隔；纯数字按分钟算 |
| `SYNC_REMOVE_ORPHANS` | `true` | 是否删除视频已不在的 xml |
| `PATH_MAP` | 空 | `Sonarr 路径前缀:本容器路径前缀`，多个用逗号分隔，最长前缀优先 |
| `IMPORT_DELAY` | `0s` | 收到 webhook 后等多久再提交（新番刚出时 Misaka 的源可能还没有这一集） |
| `TASK_WAIT_TIMEOUT` | `30m` | 等 Misaka 任务结束的最长时间；`0` 表示不等 |
| `BACKFILL_INTERVAL` | `20s` | backfill 两次提交之间的间隔 |
| `HTTP_TIMEOUT` | `60s` | 单个 HTTP 请求超时 |
| `STATE_DIR` | `/data` | 状态文件目录 |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

## 配置 Sonarr / Radarr webhook

Sonarr：设置 → Connect → `+` → Webhook

- Name：`danmu-bridge`
- 触发：勾 **On File Import**、**On File Upgrade**、**On Rename**、**On Episode File Delete**、**On Series Delete**（On Import Complete 可勾可不勾，重复的会被去重）
- Webhook URL：`http://danmu-bridge:8090/webhook/sonarr`
- Method：`POST`
- Username：`danmu`（随便填都行）；Password：填 `WEBHOOK_SECRET`
- 点 Test，日志里出现 `webhook test received from=sonarr` 就通了

Radarr：同样的位置

- 触发：勾 **On File Import**、**On File Upgrade**、**On Rename**、**On Movie File Delete**、**On Movie Delete**
- Webhook URL：`http://danmu-bridge:8090/webhook/radarr`
- Username / Password 同上

不想用 Basic Auth 的话，URL 写成 `http://danmu-bridge:8090/webhook?token=<WEBHOOK_SECRET>` 也行。

## 跑存量 backfill

```bash
# 先看会提交什么
docker exec danmu-bridge danmu-bridge backfill -dry-run
# 先试 5 个
docker exec danmu-bridge danmu-bridge backfill -limit 5
# 全量，只跑电视剧，间隔 30 秒
docker exec danmu-bridge danmu-bridge backfill -only tv -interval 30s
# 清掉进度从头来
docker exec danmu-bridge danmu-bridge backfill -reset
```

backfill 会等每个 Misaka 任务结束再提交下一个，库大的话要跑很久，中途 Ctrl-C 或重启都没关系。它和 `serve` 共用 Misaka 的搜索锁，撞上了会自动重试。

## 播放端

Jellyfin 本身不显示弹幕。网页端装弹幕脚本，手机上用支持弹弹play 接口或本地 xml 的播放器（如 SenPlayer），经 Tailscale 访问 Misaka 的弹弹play 兼容接口，或者直接读视频旁边的 xml。

## 开发

```bash
go test -race ./...
go run ./cmd/danmu-bridge sync     # 需要先设好环境变量
```

只用标准库，没有第三方依赖。

## 已知限制和没核实的地方

- 接口是按 Misaka 2026-10-30 的源码（commit `a5bb84d`）和 Sonarr v4 / Radarr 当前 develop 分支的 webhook 源码写的，还没对着你实际跑的 Misaka 版本测过。Misaka 外部接口变了的话，最可能受影响的是 `/library/source/{id}/episodes` 和 `/danmaku/{id}` 的返回字段。
- 季集匹配假设 Misaka 里的季号、集号和 Sonarr 一致。动画如果在 Misaka 里用了 TMDB 剧集组或绝对集数，可能匹配不上，日志里会计入 `noEpisode`，需要在 Misaka 里调整。
- 一个文件包含多集（如 `S01E01-E02`）时，只用第一集的弹幕。
- 如果 `sync-state.json` 丢了，之前写过的 xml 会被当成“别人的”而不再更新；删掉这些 xml 再跑一次即可重新生成。
- Dockerfile 没有在这台机器上实际构建过（没有 Docker 守护进程），arm64 二进制交叉编译通过。
