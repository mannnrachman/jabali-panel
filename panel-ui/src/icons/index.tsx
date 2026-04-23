// AntD-compatible icon shims backed by Font Awesome.
//
// AntD components accept icons via props like `icon={<SomeOutlined />}` and
// as sidebar menu item icons. To swap the glyph set without touching every
// call-site, we re-export the same AntD names but each component renders a
// FontAwesomeIcon. AntD internals (Dropdown caret, Table sort arrows, Modal
// close button) still resolve `@ant-design/icons` directly — we only replace
// application-level usage.
//
// Props mirror the subset AntD icons accepted (className, style, onClick,
// spin, rotate). `rotate` is numeric in AntD; FA accepts 90/180/270 so we
// coerce.
import type { CSSProperties, MouseEventHandler } from "react";
import { FontAwesomeIcon } from "@fortawesome/react-fontawesome";
import type { IconDefinition, RotateProp } from "@fortawesome/fontawesome-svg-core";
import {
  faArrowLeft,
  faArrowsRotate,
  faBars,
  faBolt,
  faBook,
  faCheck,
  faChevronDown,
  faChevronLeft,
  faChevronRight,
  faChevronUp,
  faCircleCheck,
  faCircleExclamation,
  faCirclePause,
  faCirclePlay,
  faClock,
  faCode,
  faCopy,
  faDatabase,
  faDownload,
  faEllipsis,
  faEnvelope,
  faEye,
  faEyeSlash,
  faFile,
  faFileLines,
  faFloppyDisk,
  faFolder,
  faGear,
  faGlobe,
  faGrip,
  faHouse,
  faInbox,
  faKey,
  faLink,
  faLock,
  faMagnifyingGlass,
  faMoon,
  faPenToSquare,
  faPlug,
  faPlus,
  faPowerOff,
  faRightFromBracket,
  faRightLeft,
  faRightToBracket,
  faRotate,
  faRotateRight,
  faServer,
  faShieldHalved,
  faSpinner,
  faSquarePlus,
  faSun,
  faTrash,
  faTriangleExclamation,
  faUpDownLeftRight,
  faUpload,
  faUser,
  faUsers,
  faWrench,
  faXmark,
} from "@fortawesome/free-solid-svg-icons";
import { faGithub } from "@fortawesome/free-brands-svg-icons";

export type IconProps = {
  className?: string;
  style?: CSSProperties;
  onClick?: MouseEventHandler<SVGSVGElement>;
  spin?: boolean;
  rotate?: number;
  title?: string;
  // AntD twotone icons pass the accent color in as a prop. We honor it
  // by pushing it into `color` on the FA icon.
  twoToneColor?: string;
};

const coerceRotate = (rotate?: number): RotateProp | undefined => {
  if (rotate === 90 || rotate === 180 || rotate === 270) return rotate;
  return undefined;
};

const shim = (icon: IconDefinition, defaults?: { spin?: boolean }) => {
  const Icon = ({ className, style, onClick, spin, rotate, title, twoToneColor }: IconProps) => {
    const mergedStyle: CSSProperties | undefined = twoToneColor
      ? { color: twoToneColor, ...(style ?? {}) }
      : style;
    return (
      <FontAwesomeIcon
        icon={icon}
        className={className}
        style={mergedStyle as never}
        onClick={onClick}
        spin={spin ?? defaults?.spin}
        rotation={coerceRotate(rotate)}
        title={title}
      />
    );
  };
  Icon.displayName = icon.iconName;
  return Icon;
};

// --- Nav / core ---
export const HomeOutlined = shim(faHouse);
export const GlobalOutlined = shim(faGlobe);
export const LockOutlined = shim(faLock);
export const CodeOutlined = shim(faCode);
export const DatabaseOutlined = shim(faDatabase);
export const FolderOutlined = shim(faFolder);
export const AppstoreOutlined = shim(faGrip);
export const AppstoreAddOutlined = shim(faSquarePlus);
export const KeyOutlined = shim(faKey);
export const ClockCircleOutlined = shim(faClock);
export const MailOutlined = shim(faEnvelope);
export const SettingOutlined = shim(faGear);
export const TeamOutlined = shim(faUsers);
export const CloudServerOutlined = shim(faServer);
export const ThunderboltOutlined = shim(faBolt);

// --- Actions ---
export const PlusOutlined = shim(faPlus);
export const PlusSquareOutlined = shim(faSquarePlus);
export const DeleteOutlined = shim(faTrash);
export const EditOutlined = shim(faPenToSquare);
export const CopyOutlined = shim(faCopy);
export const DownloadOutlined = shim(faDownload);
export const UploadOutlined = shim(faUpload);
export const SaveOutlined = shim(faFloppyDisk);
export const SearchOutlined = shim(faMagnifyingGlass);
export const ReloadOutlined = shim(faArrowsRotate);
export const SyncOutlined = shim(faRotate);
export const RedoOutlined = shim(faRotateRight);
export const ApiOutlined = shim(faPlug);
export const BookOutlined = shim(faBook);
export const DragOutlined = shim(faUpDownLeftRight);
export const LinkOutlined = shim(faLink);
export const LoginOutlined = shim(faRightToBracket);
export const LogoutOutlined = shim(faRightFromBracket);
export const MenuOutlined = shim(faBars);
export const MoreOutlined = shim(faEllipsis);
export const PoweroffOutlined = shim(faPowerOff);
export const SafetyOutlined = shim(faShieldHalved);
export const SwapOutlined = shim(faRightLeft);
export const ToolOutlined = shim(faWrench);

// --- Status ---
export const CheckOutlined = shim(faCheck);
export const CheckCircleOutlined = shim(faCircleCheck);
export const CheckCircleTwoTone = shim(faCircleCheck);
export const CloseOutlined = shim(faXmark);
export const ExclamationCircleOutlined = shim(faCircleExclamation);
export const WarningOutlined = shim(faTriangleExclamation);
export const LoadingOutlined = shim(faSpinner, { spin: true });
export const InboxOutlined = shim(faInbox);
export const PauseCircleOutlined = shim(faCirclePause);
export const PlayCircleOutlined = shim(faCirclePlay);

// --- Arrows ---
export const DownOutlined = shim(faChevronDown);
export const UpOutlined = shim(faChevronUp);
export const LeftOutlined = shim(faChevronLeft);
export const RightOutlined = shim(faChevronRight);
export const ArrowLeftOutlined = shim(faArrowLeft);

// --- Files ---
export const FileOutlined = shim(faFile);
export const FileTextOutlined = shim(faFileLines);

// --- Misc ---
export const EyeOutlined = shim(faEye);
export const EyeInvisibleOutlined = shim(faEyeSlash);
export const UserOutlined = shim(faUser);
export const MoonOutlined = shim(faMoon);
export const SunOutlined = shim(faSun);
export const GithubOutlined = shim(faGithub);
