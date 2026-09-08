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
} from '@material-ui/core'
import CloseIcon from '@material-ui/icons/Close'
import SearchIcon from '@material-ui/icons/Search'
import ViewListIcon from '@material-ui/icons/ViewList'
import { Dialogs } from '../dialogs/Dialogs'
import { AboutDialog } from '../dialogs'
import PersonalMenu from './PersonalMenu'
import ActivityPanel from './ActivityPanel'
import NowPlayingPanel from './NowPlayingPanel'
import UserMenu from './UserMenu'
import config from '../config'

const useStyles = makeStyles(
  (theme) => ({
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
      marginLeft: theme.spacing(2),
      paddingLeft: theme.spacing(1.5),
      borderRadius: 19,
      background: 'rgba(255,255,255,.16)',
      '&:focus-within': { background: 'rgba(255,255,255,.24)' },
      [theme.breakpoints.down('sm')]: { width: 190, maxWidth: '42vw' },
    },
    onlineInput: { flex: 1, color: 'inherit', fontSize: 14 },
    onlineDialog: { height: '92vh', maxHeight: '92vh' },
    onlineDialogHead: {
      display: 'flex',
      alignItems: 'center',
      minHeight: 48,
      paddingLeft: theme.spacing(2),
      borderBottom: `1px solid ${theme.palette.divider}`,
    },
    onlineDialogTitle: { flex: 1, fontWeight: 600 },
    onlineFrame: { width: '100%', height: 'calc(92vh - 49px)', border: 0 },
  }),
  {
    name: 'NDAppBar',
  },
)

const OnlineMusicSearch = () => {
  const classes = useStyles()
  const [query, setQuery] = React.useState('')
  const [open, setOpen] = React.useState(false)
  if (!config.onlineMusicURL) return null

  const search = (event) => {
    event.preventDefault()
    if (query.trim()) setOpen(true)
  }
  const separator = config.onlineMusicURL.includes('?') ? '&' : '?'
  const src = `${config.onlineMusicURL}${separator}embed=1&q=${encodeURIComponent(
    query.trim(),
  )}`

  return (
    <>
      <form className={classes.onlineSearch} onSubmit={search}>
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
        onClose={() => setOpen(false)}
        PaperProps={{ className: classes.onlineDialog }}
      >
        <div className={classes.onlineDialogHead}>
          <span className={classes.onlineDialogTitle}>在线音乐搜索</span>
          <IconButton onClick={() => setOpen(false)} aria-label="关闭">
            <CloseIcon />
          </IconButton>
        </div>
        <iframe className={classes.onlineFrame} src={src} title="在线音乐搜索" />
      </Dialog>
    </>
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
    <OnlineMusicSearch />
  </RAAppBar>
)

export default AppBar
