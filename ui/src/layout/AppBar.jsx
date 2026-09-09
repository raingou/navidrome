import React, { createElement, forwardRef, Fragment } from 'react'
import {
  AppBar as RAAppBar,
  MenuItemLink,
  useTranslate,
  usePermissions,
  getResources,
} from 'react-admin'
import { MdInfo, MdPerson, MdSupervisorAccount } from 'react-icons/md'
import { useSelector } from 'react-redux'
import {
  makeStyles,
  MenuItem,
  ListItemIcon,
  Divider,
  Dialog,
  IconButton,
  InputBase,
  Tooltip,
  Button,
  CircularProgress,
  DialogContent,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Select,
  LinearProgress,
} from '@material-ui/core'
import CloseIcon from '@material-ui/icons/Close'
import SearchIcon from '@material-ui/icons/Search'
import PlayArrowIcon from '@material-ui/icons/PlayArrow'
import StopIcon from '@material-ui/icons/Stop'
import GetAppIcon from '@material-ui/icons/GetApp'
import ViewListIcon from '@material-ui/icons/ViewList'
import CheckCircleIcon from '@material-ui/icons/CheckCircle'
import ErrorIcon from '@material-ui/icons/Error'
import { Dialogs } from '../dialogs/Dialogs'
import { AboutDialog } from '../dialogs'
import PersonalMenu from './PersonalMenu'
import ActivityPanel from './ActivityPanel'
import NowPlayingPanel from './NowPlayingPanel'
import UserMenu from './UserMenu'
import config from '../config'
import httpClient from '../dataProvider/httpClient'
import { baseUrl } from '../utils'

const useStyles = makeStyles(
  (theme) => ({
    '@global': {
      '[class*="RaSearchInput-input"] .MuiOutlinedInput-root': {
        minHeight: 38,
        borderRadius: 19,
        backgroundColor: theme.palette.action.hover,
      },
      '[class*="RaSearchInput-input"] .MuiOutlinedInput-input': {
        paddingTop: 9,
        paddingBottom: 9,
      },
    },
    root: {
      color: theme.palette.text.secondary,
    },
    active: {
      color: theme.palette.text.primary,
    },
    icon: { minWidth: theme.spacing(5) },
    onlineSearch: {
      display: 'flex',
      alignItems: 'center',
      width: 360,
      maxWidth: '38vw',
      height: 38,
      marginLeft: 0,
      marginRight: 0,
      paddingLeft: theme.spacing(1.5),
      borderRadius: 19,
      background: 'rgba(255,255,255,.16)',
      '&:focus-within': { background: 'rgba(255,255,255,.24)' },
      [theme.breakpoints.down('sm')]: { width: 190, maxWidth: '42vw' },
    },
    onlineInput: { flex: 1, color: 'inherit', fontSize: 14 },
    providerSelect: {
      color: 'inherit',
      fontSize: 13,
      marginRight: theme.spacing(1),
      '&:before, &:after': { display: 'none' },
      '& .MuiSelect-icon': { color: 'inherit' },
    },
    onlineDialog: { height: '92vh', maxHeight: '92vh' },
    onlineDialogHead: {
      display: 'flex',
      alignItems: 'center',
      minHeight: 48,
      paddingLeft: theme.spacing(2),
      borderBottom: `1px solid ${theme.palette.divider}`,
    },
    onlineDialogTitle: { flex: 1, fontWeight: 600 },
    onlineContent: { padding: 0, overflow: 'auto' },
    onlineCover: { width: 42, height: 42, borderRadius: 4, objectFit: 'cover' },
    onlineActions: { whiteSpace: 'nowrap' },
    downloadProgress: {
      width: 118,
      display: 'inline-block',
      verticalAlign: 'middle',
    },
    localRow: { backgroundColor: theme.palette.action.selected },
    sectionRow: {
      fontWeight: 600,
      backgroundColor: theme.palette.background.default,
    },
    onlineMessage: { padding: theme.spacing(4), textAlign: 'center' },
    headerLayout: {
      display: 'grid',
      gridTemplateColumns: (props) =>
        props.sidebarOpen
          ? '184px minmax(300px, 420px) minmax(40px, 1fr)'
          : '112px minmax(260px, 420px) minmax(40px, 1fr)',
      alignItems: 'center',
      flex: 1,
      minWidth: 0,
      [theme.breakpoints.down('sm')]: {
        gridTemplateColumns: '82px minmax(150px, 1fr) 8px',
      },
    },
    systemName: {
      paddingLeft: theme.spacing(1),
      fontSize: 20,
      fontWeight: 600,
      whiteSpace: 'nowrap',
      [theme.breakpoints.down('sm')]: { fontSize: 16 },
    },
  }),
  {
    name: 'NDAppBar',
  },
)

const OnlineMusicSearch = () => {
  const classes = useStyles()
  const [query, setQuery] = React.useState('')
  const [provider, setProvider] = React.useState('all')
  const [open, setOpen] = React.useState(false)
  const [loading, setLoading] = React.useState(false)
  const [items, setItems] = React.useState([])
  const [error, setError] = React.useState('')
  const [playingId, setPlayingId] = React.useState(null)
  const [downloadStates, setDownloadStates] = React.useState({})
  const audio = React.useRef(null)

  const search = async (event) => {
    event.preventDefault()
    if (!query.trim()) return
    setOpen(true)
    setLoading(true)
    setError('')
    try {
      const response = await httpClient(
        `/api/online-music/search?q=${encodeURIComponent(
          query.trim(),
        )}&provider=${encodeURIComponent(provider)}`,
      )
      setItems(response.json?.items || [])
    } catch (searchError) {
      setItems([])
      setError(searchError?.message || '在线搜索失败')
    } finally {
      setLoading(false)
    }
  }

  const togglePlay = (item) => {
    if (playingId === item.id) {
      audio.current?.pause()
      setPlayingId(null)
      return
    }
    audio.current?.pause()
    const token = localStorage.getItem('token') || ''
    const player = new Audio(
      baseUrl(
        `/api/online-music/stream?id=${encodeURIComponent(
          item.id,
        )}&provider=${encodeURIComponent(item.provider)}&jwt=${encodeURIComponent(
          token,
        )}`,
      ),
    )
    player.addEventListener('ended', () => setPlayingId(null), { once: true })
    audio.current = player
    setPlayingId(item.id)
    player.play().catch(() => {
      setPlayingId(null)
      setError('该歌曲暂时无法试听')
    })
  }

  const itemKey = (item) => `${item.provider}:${item.id}`

  const importSong = async (item) => {
    const key = itemKey(item)
    setDownloadStates((current) => ({
      ...current,
      [key]: { status: 'preparing', progress: 2 },
    }))
    setError('')
    try {
      const started = await httpClient('/api/online-music/import/start', {
        method: 'POST',
        body: JSON.stringify(item),
        headers: new Headers({ 'Content-Type': 'application/json' }),
      })
      const jobId = started.json?.jobId
      if (!jobId) throw new Error('无法启动下载任务')
      let finished = false
      while (!finished) {
        await new Promise((resolve) => setTimeout(resolve, 700))
        const response = await httpClient(
          `/api/online-music/import/status/${jobId}`,
        )
        const state = response.json || {}
        setDownloadStates((current) => ({ ...current, [key]: state }))
        finished = state.status === 'completed' || state.status === 'failed'
        if (state.status === 'failed')
          throw new Error(state.error || '下载失败')
      }
    } catch (importError) {
      setDownloadStates((current) => ({
        ...current,
        [key]: { status: 'failed', error: importError?.message || '下载失败' },
      }))
      setError(importError?.message || '下载入库失败，请检查音乐目录写入权限')
    }
  }

  const close = () => {
    audio.current?.pause()
    setPlayingId(null)
    setOpen(false)
  }

  return (
    <>
      <form className={classes.onlineSearch} onSubmit={search}>
        <Select
          native
          className={classes.providerSelect}
          value={provider}
          onChange={(event) => setProvider(event.target.value)}
          inputProps={{ 'aria-label': '音乐来源' }}
        >
          <option value="all">全部来源</option>
          <option value="netease">网易云</option>
          <option value="qq">QQ音乐</option>
          <option value="kugou">酷狗</option>
        </Select>
        <InputBase
          className={classes.onlineInput}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder="在线搜索歌曲、歌手或专辑"
          inputProps={{ 'aria-label': '在线搜索音乐' }}
        />
        <Tooltip title="在线搜索">
          <IconButton color="inherit" size="small" type="submit">
            <SearchIcon />
          </IconButton>
        </Tooltip>
      </form>
      <Dialog
        fullWidth
        maxWidth="lg"
        open={open}
        onClose={close}
        PaperProps={{ className: classes.onlineDialog }}
      >
        <div className={classes.onlineDialogHead}>
          <span className={classes.onlineDialogTitle}>在线音乐搜索</span>
          <IconButton onClick={close} aria-label="关闭">
            <CloseIcon />
          </IconButton>
        </div>
        <DialogContent className={classes.onlineContent}>
          {loading ? (
            <div className={classes.onlineMessage}>
              <CircularProgress />
            </div>
          ) : error && items.length === 0 ? (
            <div className={classes.onlineMessage}>{error}</div>
          ) : items.length === 0 ? (
            <div className={classes.onlineMessage}>没有找到相关歌曲</div>
          ) : (
            <Table stickyHeader size="small">
              <TableHead>
                <TableRow>
                  <TableCell>封面</TableCell>
                  <TableCell>歌曲</TableCell>
                  <TableCell>歌手</TableCell>
                  <TableCell>专辑</TableCell>
                  <TableCell>来源</TableCell>
                  <TableCell align="right">操作</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {items.map((item, index) => {
                  const state = downloadStates[itemKey(item)] || {}
                  const firstOnline =
                    index > 0 && !item.local && items[index - 1]?.local
                  return (
                    <React.Fragment key={itemKey(item)}>
                      {index === 0 && item.local ? (
                        <TableRow>
                          <TableCell className={classes.sectionRow} colSpan={6}>
                            本地音乐（已存在，无需重复下载）
                          </TableCell>
                        </TableRow>
                      ) : null}
                      {firstOnline ? (
                        <TableRow>
                          <TableCell className={classes.sectionRow} colSpan={6}>
                            在线音乐来源
                          </TableCell>
                        </TableRow>
                      ) : null}
                      <TableRow
                        hover
                        className={item.local ? classes.localRow : undefined}
                      >
                        <TableCell>
                          {item.cover ? (
                            <img
                              className={classes.onlineCover}
                              src={item.cover}
                              alt=""
                            />
                          ) : null}
                        </TableCell>
                        <TableCell>{item.title}</TableCell>
                        <TableCell>{item.artist}</TableCell>
                        <TableCell>{item.album}</TableCell>
                        <TableCell>
                          {{
                            local: '本地音乐',
                            netease: '网易云',
                            qq: 'QQ音乐',
                            kugou: '酷狗',
                          }[item.provider] || item.provider}
                        </TableCell>
                        <TableCell
                          align="right"
                          className={classes.onlineActions}
                        >
                          {!item.local ? (
                            <Tooltip
                              title={
                                playingId === item.id ? '停止试听' : '在线试听'
                              }
                            >
                              <IconButton onClick={() => togglePlay(item)}>
                                {playingId === item.id ? (
                                  <StopIcon />
                                ) : (
                                  <PlayArrowIcon />
                                )}
                              </IconButton>
                            </Tooltip>
                          ) : null}
                          {item.local ? (
                            <Button
                              size="small"
                              disabled
                              startIcon={<CheckCircleIcon />}
                            >
                              本地已有
                            </Button>
                          ) : state.status === 'downloading' ||
                            state.status === 'preparing' ? (
                            <span className={classes.downloadProgress}>
                              <LinearProgress
                                variant={
                                  state.progress
                                    ? 'determinate'
                                    : 'indeterminate'
                                }
                                value={state.progress || 0}
                              />
                              <small>
                                {state.status === 'preparing'
                                  ? '准备下载…'
                                  : `下载中 ${state.progress || 0}%`}
                              </small>
                            </span>
                          ) : state.status === 'completed' ? (
                            <Button
                              size="small"
                              disabled
                              startIcon={<CheckCircleIcon />}
                            >
                              下载完成
                            </Button>
                          ) : state.status === 'failed' ? (
                            <Button
                              size="small"
                              color="secondary"
                              startIcon={<ErrorIcon />}
                              onClick={() => importSong(item)}
                            >
                              失败，重试
                            </Button>
                          ) : (
                            <Button
                              size="small"
                              startIcon={<GetAppIcon />}
                              onClick={() => importSong(item)}
                            >
                              下载入库
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    </React.Fragment>
                  )
                })}
              </TableBody>
            </Table>
          )}
          {error && items.length > 0 ? (
            <div className={classes.onlineMessage}>{error}</div>
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  )
}

const HeaderContent = () => {
  const sidebarOpen = useSelector((state) => state.admin.ui.sidebarOpen)
  const classes = useStyles({ sidebarOpen })
  return (
    <div className={classes.headerLayout}>
      <span className={classes.systemName}>音乐</span>
      <OnlineMusicSearch />
      <span />
    </div>
  )
}

const AboutMenuItem = forwardRef(({ onClick, ...rest }, ref) => {
  const classes = useStyles(rest)
  const translate = useTranslate()
  const [open, setOpen] = React.useState(false)

  const handleOpen = () => {
    setOpen(true)
  }
  const handleClose = () => {
    onClick && onClick()
    setOpen(false)
  }
  const label = translate('menu.about')
  return (
    <>
      <MenuItem ref={ref} onClick={handleOpen} className={classes.root}>
        <ListItemIcon className={classes.icon}>
          <MdInfo title={label} size={24} />
        </ListItemIcon>
        {label}
      </MenuItem>
      <AboutDialog onClose={handleClose} open={open} />
    </>
  )
})

AboutMenuItem.displayName = 'AboutMenuItem'

const settingsResources = (resource) =>
  resource.name !== 'user' &&
  resource.hasList &&
  resource.options &&
  resource.options.subMenu === 'settings'

const CustomUserMenu = ({ onClick, ...rest }) => {
  const translate = useTranslate()
  const resources = useSelector(getResources)
  const classes = useStyles(rest)
  const { permissions } = usePermissions()

  const resourceDefinition = (resourceName) =>
    resources.find((r) => r?.name === resourceName)

  const renderUserMenuItemLink = () => {
    const userResource = resourceDefinition('user')
    if (!userResource) {
      return null
    }
    if (permissions !== 'admin') {
      if (!config.enableUserEditing) {
        return null
      }
      userResource.icon = MdPerson
    } else {
      userResource.icon = MdSupervisorAccount
    }
    return renderSettingsMenuItemLink(
      userResource,
      permissions !== 'admin' ? localStorage.getItem('userId') : null,
    )
  }

  const renderSettingsMenuItemLink = (resource, id) => {
    const label = translate(`resources.${resource.name}.name`, {
      smart_count: id ? 1 : 2,
    })
    const link = id ? `/${resource.name}/${id}` : `/${resource.name}`
    return (
      <MenuItemLink
        className={classes.root}
        activeClassName={classes.active}
        key={resource.name}
        to={link}
        primaryText={label}
        leftIcon={
          (resource.icon && createElement(resource.icon, { size: 24 })) || (
            <ViewListIcon />
          )
        }
        onClick={onClick}
        sidebarIsOpen={true}
      />
    )
  }

  return (
    <>
      {config.devActivityPanel &&
        permissions === 'admin' &&
        config.enableNowPlaying && <NowPlayingPanel />}
      {config.devActivityPanel && permissions === 'admin' && <ActivityPanel />}
      <UserMenu {...rest}>
        <PersonalMenu sidebarIsOpen={true} onClick={onClick} />
        <Divider />
        {renderUserMenuItemLink()}
        {resources
          .filter(settingsResources)
          .map((r) => renderSettingsMenuItemLink(r))}
        <Divider />
        <AboutMenuItem />
      </UserMenu>
      <Dialogs />
    </>
  )
}

const AppBar = (props) => (
  <RAAppBar {...props} container={Fragment} userMenu={<CustomUserMenu />}>
    <HeaderContent />
  </RAAppBar>
)

export default AppBar
