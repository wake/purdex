export const WORKSPACE_COLORS = [
  '#7a6aaa', '#6aaa7a', '#aa6a7a', '#6a8aaa', '#aa8a6a', '#8a6aaa',
  '#5b8c5a', '#c75050', '#d4a843', '#5a7fbf', '#bf5a9d', '#4abfbf',
]

/** Curated Phosphor icon names by category */
export const CURATED_ICON_CATEGORIES: Record<string, string[]> = {
  general: [
    'House', 'Star', 'Heart', 'Bell', 'Flag', 'Lightning', 'Fire',
    'BookmarkSimple', 'Crown', 'Diamond', 'Eye', 'Fingerprint', 'Gift',
    'Globe', 'Handshake', 'Key', 'Lightbulb', 'MagnifyingGlass', 'Medal',
    'PushPin', 'Shield', 'Sparkle', 'Tag', 'Trophy', 'Umbrella',
  ],
  development: [
    'Terminal', 'Code', 'GitBranch', 'GitCommit', 'GitPullRequest', 'Bug',
    'Database', 'CloudArrowUp', 'Cpu', 'HardDrive', 'Plugs', 'Robot',
    'Atom', 'Brackets', 'BracketsAngle', 'BracketsSquare', 'CodeBlock',
    'DeviceMobile', 'Flask', 'Function', 'Hash', 'Infinity', 'Plug',
    'Pulse', 'WebhooksLogo',
  ],
  objects: [
    'Folder', 'FolderOpen', 'File', 'FileText', 'Clipboard', 'Book',
    'BookOpen', 'Lock', 'LockOpen', 'Wrench', 'Gear', 'Hammer',
    'Scissors', 'Pencil', 'Pen', 'Eraser', 'Paperclip', 'Archive',
    'Bag', 'Basket', 'Cube', 'Briefcase', 'Package', 'Suitcase', 'Wallet',
  ],
  communication: [
    'ChatCircle', 'ChatDots', 'ChatText', 'Envelope', 'EnvelopeOpen',
    'Phone', 'PhoneCall', 'Megaphone', 'Broadcast', 'Rss', 'At',
    'Link', 'PaperPlane', 'Share', 'ShareNetwork', 'Chats', 'Handshake',
    'SpeakerHigh', 'Microphone', 'VideoCamera', 'Headphones',
    'ChatCircleDots', 'ChatTeardrop', 'Translate', 'UserCircle',
  ],
  media: [
    'Play', 'Pause', 'Stop', 'Camera', 'MusicNote', 'MusicNotes',
    'Image', 'FilmSlate', 'Monitor', 'Desktop', 'Tv', 'Radio',
    'Headphones', 'SpeakerHigh', 'Microphone', 'Record', 'Disc',
    'Playlist', 'Queue', 'Repeat', 'Shuffle', 'SkipForward',
    'Screencast', 'PictureInPicture', 'Aperture',
  ],
  arrows: [
    'ArrowRight', 'ArrowLeft', 'ArrowUp', 'ArrowDown', 'ArrowsClockwise',
    'ArrowSquareOut', 'ArrowBendUpRight', 'CaretRight', 'Compass',
    'MapPin', 'NavigationArrow', 'Signpost', 'Path', 'Crosshair',
    'Target', 'ArrowFatRight', 'ArrowCircleRight', 'ArrowElbowRight',
    'ArrowLineRight', 'ArrowUUpRight', 'Cursor', 'CursorClick',
    'GitDiff', 'Swap', 'Shuffle',
  ],
  nature: [
    'Sun', 'Moon', 'Cloud', 'CloudSun', 'CloudRain', 'Snowflake',
    'Tree', 'Leaf', 'Flower', 'Drop', 'Wind', 'Thermometer',
    'Mountains', 'Waves', 'Rainbow', 'Planet', 'Grains', 'PawPrint',
    'Bird', 'Butterfly', 'Cat', 'Dog', 'Fish', 'Horse',
    'Bug', 'Cactus',
  ],
  business: [
    'ChartBar', 'ChartLine', 'ChartPie', 'Calendar', 'CalendarCheck',
    'Money', 'CurrencyDollar', 'Bank', 'Buildings', 'Storefront',
    'Receipt', 'Invoice', 'CreditCard', 'Scales', 'Gavel',
    'Presentation', 'Strategy', 'TrendUp', 'TrendDown', 'Percent',
    'Calculator', 'ClipboardText', 'Newspaper', 'Kanban', 'ListChecks',
  ],
}

/** Flat set of all curated icon names for quick lookup */
export const CURATED_ICON_SET = new Set(
  Object.values(CURATED_ICON_CATEGORIES).flat()
)
