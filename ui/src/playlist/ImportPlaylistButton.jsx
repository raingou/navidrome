import React, { useRef, useState } from 'react'
import {
  Button,
  Checkbox,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Typography,
} from '@material-ui/core'
import GetAppIcon from '@material-ui/icons/GetApp'
import { useNotify, useRefresh } from 'react-admin'
import httpClient from '../dataProvider/httpClient'

const splitTrack = (value) => {
  const text = value.trim().replace(/^['"]|['"]$/g, '')
  const parts = text.split(/\s+-\s+|\s+–\s+/)
  return parts.length > 1
    ? { artist: parts.shift().trim(), title: parts.join(' - ').trim() }
    : { title: text, artist: '' }
}

const parsePlaylist = (text, filename) => {
  const lines = text.replace(/^\uFEFF/, '').split(/\r?\n/)
  const tracks = []
  let extInfo = null
  if (/\.csv$/i.test(filename) && lines.length) {
    const parseRow = (line) =>
      line
        .split(/,(?=(?:[^"]*"[^"]*")*[^"]*$)/)
        .map((v) => v.trim().replace(/^"|"$/g, '').replace(/""/g, '"'))
    const header = parseRow(lines[0]).map((v) => v.toLowerCase())
    const titleIndex = header.findIndex((v) =>
      /^(track|track name|title|song|歌曲|歌名)$/.test(v),
    )
    const artistIndex = header.findIndex((v) => /artist|歌手/.test(v))
    if (titleIndex >= 0) {
      lines.slice(1).forEach((line) => {
        const row = parseRow(line)
        if (row[titleIndex])
          tracks.push({
            title: row[titleIndex],
            artist: artistIndex >= 0 ? row[artistIndex] || '' : '',
          })
      })
      return tracks
    }
  }
  lines.forEach((line) => {
    const value = line.trim()
    if (!value) return
    if (value.startsWith('#EXTINF:')) {
      extInfo = splitTrack(value.slice(value.indexOf(',') + 1))
    } else if (!value.startsWith('#')) {
      if (extInfo) tracks.push(extInfo)
      else if (!/^(https?:|file:|[a-z]:\\|\/)/i.test(value))
        tracks.push(
          splitTrack(
            value.replace(/\.[a-z0-9]{2,5}$/i, '').replace(/^.*[\\/]/, ''),
          ),
        )
      extInfo = null
    }
  })
  return tracks
}

export const ImportPlaylistButton = () => {
  const input = useRef()
  const notify = useNotify()
  const refresh = useRefresh()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [tracks, setTracks] = useState([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  const chooseFile = () => input.current?.click()
  const loadFile = async (event) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    const parsed = parsePlaylist(await file.text(), file.name)
    if (!parsed.length)
      return notify(
        '没有识别到歌曲，请使用 CSV、M3U/M3U8 或“歌手 - 歌名”TXT 文件',
        'warning',
      )
    setLoading(true)
    setName(file.name.replace(/\.(csv|m3u8?|txt)$/i, ''))
    setOpen(true)
    try {
      const response = await httpClient('/api/online-music/playlist-match', {
        method: 'POST',
        headers: new Headers({ 'Content-Type': 'application/json' }),
        body: JSON.stringify({ name: file.name, tracks: parsed }),
      })
      const matched = response.json?.tracks || []
      const enriched = []
      for (const item of matched) {
        if (item.matched) enriched.push({ ...item, selected: true })
        else {
          try {
            const search = await httpClient(
              `/api/online-music/search?q=${encodeURIComponent(`${item.title} ${item.artist}`)}&provider=all&limit=3`,
            )
            enriched.push({
              ...item,
              online: search.json?.items?.[0] || null,
              selected: !!search.json?.items?.[0],
            })
          } catch (_) {
            enriched.push({ ...item, online: null, selected: false })
          }
        }
      }
      setTracks(enriched)
    } catch (error) {
      notify(error?.message || '歌单匹配失败', 'warning')
      setOpen(false)
    } finally {
      setLoading(false)
    }
  }

  const save = async () => {
    setSaving(true)
    try {
      const selected = tracks.filter((track) => track.selected)
      const resolved = []
      for (const track of selected) {
        if (track.path) resolved.push(track)
        else if (track.online) {
          const download = await httpClient('/api/online-music/import', {
            method: 'POST',
            headers: new Headers({ 'Content-Type': 'application/json' }),
            body: JSON.stringify(track.online),
          })
          if (download.json?.file)
            resolved.push({ ...track, path: download.json.file })
        }
      }
      const response = await httpClient('/api/online-music/playlist-import', {
        method: 'POST',
        headers: new Headers({ 'Content-Type': 'application/json' }),
        body: JSON.stringify({ name, tracks: resolved }),
      })
      notify(
        `已导入歌单“${response.json?.name || name}”，共 ${response.json?.tracks || resolved.length} 首；扫描后会显示在歌单菜单中`,
        'info',
      )
      setOpen(false)
      refresh()
    } catch (error) {
      notify(error?.message || '歌单导入失败', 'warning')
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <input
        ref={input}
        type="file"
        hidden
        accept=".csv,.m3u,.m3u8,.txt,text/csv,audio/x-mpegurl"
        onChange={loadFile}
      />
      <Button color="primary" onClick={chooseFile} startIcon={<GetAppIcon />}>
        导入外部歌单
      </Button>
      <Dialog
        fullWidth
        maxWidth="md"
        open={open}
        onClose={() => !saving && setOpen(false)}
      >
        <DialogTitle>导入外部歌单：{name}</DialogTitle>
        <DialogContent>
          {loading ? (
            <div style={{ textAlign: 'center', padding: 40 }}>
              <CircularProgress />
              <Typography>正在匹配本地音乐和在线来源…</Typography>
            </div>
          ) : (
            <Table size="small">
              <TableHead>
                <TableRow>
                  <TableCell padding="checkbox" />
                  <TableCell>原歌单歌曲</TableCell>
                  <TableCell>匹配结果</TableCell>
                  <TableCell>处理方式</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {tracks.map((track, index) => (
                  <TableRow key={`${track.title}-${index}`}>
                    <TableCell padding="checkbox">
                      <Checkbox
                        checked={!!track.selected}
                        disabled={!track.path && !track.online}
                        onChange={(e) =>
                          setTracks((old) =>
                            old.map((v, i) =>
                              i === index
                                ? { ...v, selected: e.target.checked }
                                : v,
                            ),
                          )
                        }
                      />
                    </TableCell>
                    <TableCell>
                      {track.title}
                      <br />
                      <small>{track.artist}</small>
                    </TableCell>
                    <TableCell>
                      {track.path
                        ? `${track.localTitle} — ${track.localArtist}`
                        : track.online
                          ? `${track.online.title} — ${track.online.artist}（${track.online.provider}）`
                          : '未找到'}
                    </TableCell>
                    <TableCell>
                      {track.path
                        ? '使用本地歌曲'
                        : track.online
                          ? '确认后下载歌曲和歌词'
                          : '跳过'}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </DialogContent>
        <DialogActions>
          <Button disabled={saving} onClick={() => setOpen(false)}>
            取消
          </Button>
          <Button
            color="primary"
            variant="contained"
            disabled={loading || saving || !tracks.some((t) => t.selected)}
            onClick={save}
          >
            {saving ? '正在下载并创建…' : '确认导入'}
          </Button>
        </DialogActions>
      </Dialog>
    </>
  )
}
