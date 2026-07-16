import type { FC } from 'react'
import type { LucideIcon } from 'lucide-react'
import {
  BookOpen,
  Box,
  Brain,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  Clock3,
  Copy,
  Database,
  Download,
  Eye,
  EyeOff,
  FileText,
  FolderPlus,
  Info,
  KeyRound,
  LoaderCircle,
  LockKeyhole,
  MessageSquare,
  Moon,
  MoreHorizontal,
  PanelLeftClose,
  PanelLeftOpen,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Send,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  Sun,
  Trash2,
  TriangleAlert,
  Upload,
  UserRound,
  X,
  Zap,
} from 'lucide-react'

export type AppIconName =
  | 'alert'
  | 'book'
  | 'box'
  | 'brain'
  | 'check'
  | 'chevronDown'
  | 'chevronLeft'
  | 'chevronRight'
  | 'chevronUp'
  | 'clock'
  | 'copy'
  | 'database'
  | 'download'
  | 'eye'
  | 'eyeOff'
  | 'file'
  | 'folderPlus'
  | 'info'
  | 'key'
  | 'loader'
  | 'lock'
  | 'message'
  | 'moon'
  | 'more'
  | 'panelClose'
  | 'panelOpen'
  | 'pencil'
  | 'plus'
  | 'refresh'
  | 'search'
  | 'send'
  | 'settings'
  | 'shield'
  | 'sliders'
  | 'sparkles'
  | 'sun'
  | 'trash'
  | 'upload'
  | 'user'
  | 'x'
  | 'zap'

const icons: Record<AppIconName, LucideIcon> = {
  alert: TriangleAlert,
  book: BookOpen,
  box: Box,
  brain: Brain,
  check: Check,
  chevronDown: ChevronDown,
  chevronLeft: ChevronLeft,
  chevronRight: ChevronRight,
  chevronUp: ChevronUp,
  clock: Clock3,
  copy: Copy,
  database: Database,
  download: Download,
  eye: Eye,
  eyeOff: EyeOff,
  file: FileText,
  folderPlus: FolderPlus,
  info: Info,
  key: KeyRound,
  loader: LoaderCircle,
  lock: LockKeyhole,
  message: MessageSquare,
  moon: Moon,
  more: MoreHorizontal,
  panelClose: PanelLeftClose,
  panelOpen: PanelLeftOpen,
  pencil: Pencil,
  plus: Plus,
  refresh: RefreshCw,
  search: Search,
  send: Send,
  settings: Settings,
  shield: ShieldCheck,
  sliders: SlidersHorizontal,
  sparkles: Sparkles,
  sun: Sun,
  trash: Trash2,
  upload: Upload,
  user: UserRound,
  x: X,
  zap: Zap,
}

interface AppIconProps {
  className?: string
  name: AppIconName
  size?: number
  strokeWidth?: number
}

const AppIcon: FC<AppIconProps> = ({
  className,
  name,
  size = 18,
  strokeWidth = 1.75,
}) => {
  const Icon = icons[name]
  return <Icon aria-hidden="true" className={className} size={size} strokeWidth={strokeWidth} />
}

export default AppIcon
