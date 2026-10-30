# libretv（带 Misaka 弹幕）

[LibreTV](https://github.com/LibreSpark/LibreTV) 是网页版的影视聚合播放器：搜第三方采集站、在浏览器里播 HLS。上游没有弹幕功能，这里用 `danmaku.patch` 给它加上，弹幕来自弹幕层里的 Misaka。

```
浏览器 ──https──▶ libretv-ts (Tailscale) ──▶ LibreTV :8080
                                              │ /api/danmaku?title=…&episode=N
                                              ▼
                               http://misaka:7768/api/v1/<TOKEN>/api/v2
                               /match → /search/episodes → /comment/{id}
```

和片库无关：LibreTV 播的是采集站的片，不是 Jellyfin 里的。片库继续用 Jellyfin + dd-danmaku。

## 补丁做了什么

- 新接口 `/api/danmaku`（走 LibreTV 自己的登录校验）。服务端拿“剧名 第N集”（电影用“剧名 (年份)”）调 Misaka 的 `/match`；没中再用 `/search/episodes` 按剧名 + 集号查库；然后取 `/comment/{episodeId}`，转成 ArtPlayer 弹幕插件的格式。匹配结果在内存里缓存 6 小时（没匹配上只缓存 10 分钟）。
- 播放器挂 `artplayer-plugin-danmuku`，每集拉一次，换集先清空，迟到的上一集结果丢掉。
- 没设 `MISAKA_URL` / `MISAKA_TOKEN` 时完全不挂插件，和原版一样。
- token 只在服务端用，不下发浏览器；浏览器只和 LibreTV 同源通信。

采集站不给豆瓣 / TMDB ID，只有剧名、年份和第几集，所以只能按名字匹配。

## 版本

`Dockerfile` 里的 `LIBRETV_COMMIT` 固定上游提交（当前 `29cbeb3`，v2.21.1）。补丁在这个提交上测过：类型检查和上游全部单测（含新增的 4 个）通过，`npm ci` 能装上。

升级上游：

```bash
git clone https://github.com/LibreSpark/LibreTV.git && cd LibreTV
git checkout <新提交>
git am <本仓库>/libretv/danmaku.patch      # 冲突多半在 src/components/player-shell.tsx
npm install && npx tsc --noEmit && npm test
git format-patch -1 --stdout > <本仓库>/libretv/danmaku.patch
```

再把 `LIBRETV_COMMIT` 改成新提交，重新 build。

## 构建和部署

在 `references/compose-danmaku.yml` 里（profile `libretv`，默认不启动），步骤见 `references/wiring.md` §7f。

```bash
docker build -t libretv-danmaku:latest <本仓库>/libretv
# 在中国大陆构建可以改用镜像源：
#   docker build --build-arg NPM_REGISTRY=https://registry.npmmirror.com/ -t libretv-danmaku:latest <本仓库>/libretv
```

| 环境变量 | 说明 |
| --- | --- |
| `PASSWORD` | 必填，LibreTV 的访问密码 |
| `MISAKA_URL` | 同一 docker 网络里用 `http://misaka:7768` |
| `MISAKA_TOKEN` | Misaka「弹幕 → Token管理」里给 LibreTV 单独建一个 |
| `PROXY_SECRET` | 可选，会话签名密钥；不设时由 `PASSWORD` 派生，改密码会让所有人重新登录 |

其他变量（预置采集站、直播源等）见上游 README。

## 状态

无状态：观看记录、设置都在浏览器（IndexedDB / localStorage），服务端不落盘，不需要备份。

## 已知问题

- 同名剧、翻拍、季号写法不统一（“第二季” / “2” / “S2”）可能配错或配不上；采集站集号偶尔和平台不一致（混了预告、花絮）。在 Misaka 后台纠正一次，之后就固定了。
- 库里没有的剧，第一次要等 Misaka 全网搜索导入，可能几十秒（Misaka 默认最多等 60 秒）。
- 后备匹配让 Misaka 去爬各平台，频繁换剧会给它和平台压力；被限流时弹幕会缺。
