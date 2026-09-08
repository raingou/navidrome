# Navidrome 在线搜索与歌词

本分支在 Navidrome 顶部标题栏加入在线搜索框，并通过 `raingou/musicdown`
提供多来源搜索、试听和导入。导入歌曲时会将音频及同名 `.lrc` 写入 Navidrome
音乐目录；Navidrome 的文件监视器随后自动扫描并显示歌词。

## 部署

在服务器创建 `.env`：

```env
MUSIC_DIR=/服务器现有音乐目录
NAVIDROME_DATA_DIR=/服务器现有Navidrome数据目录
NAVIDROME_PORT=4533
ONLINE_MUSIC_PORT=3000
ONLINE_MUSIC_PUBLIC_URL=http://服务器局域网IP或域名:3000
```

`ONLINE_MUSIC_PUBLIC_URL` 是浏览器能访问的地址，不能填写 Docker 服务名。
然后运行：

```bash
docker compose -f docker-compose.integrated.yml up -d --build
```

在线音乐容器必须对 `MUSIC_DIR` 有写入权限；Navidrome 继续以只读方式挂载该目录。
导入后文件按 `歌手/专辑/歌名.格式` 和 `歌手/专辑/歌名.lrc` 保存。

