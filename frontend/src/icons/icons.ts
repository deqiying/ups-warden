// 图标路径由 docs/design/assets/apple-ui.js 迁移（69 个，24×24 描边，参照 SF Symbols 比例自绘）。
// 修改或新增图标时同步效果图脚本，保持两边一致。
export const ICON_PATHS = {
  grid: '<rect x="3.5" y="3.5" width="7" height="7" rx="1.8"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.8"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.8"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.8"/>',
  ups: '<rect x="6" y="2.5" width="12" height="19" rx="2.6"/><path d="M9.5 6.8h5M9.5 10h5"/><path d="m12.6 13.2-1.9 2.8h2.6l-1.9 2.8"/>',
  bell: '<path d="M6 16.5V11a6 6 0 0 1 12 0v5.5l1.5 2h-15z"/><path d="M10 20.8a2.2 2.2 0 0 0 4 0"/>',
  branch:
    '<path d="M7 20.5V4"/><path d="m4 7 3-3 3 3"/><path d="M7 9.5a5.5 5.5 0 0 0 5.5 5.5H18"/><path d="m15.5 12.5 2.5 2.5-2.5 2.5"/>',
  power: '<path d="M12 3v8.5"/><path d="M7.05 6.3a7.2 7.2 0 1 0 9.9 0"/>',
  desktop: '<rect x="3" y="4" width="18" height="12.5" rx="2.2"/><path d="M9 20.5h6M12 16.5v4"/>',
  phone: '<rect x="6.5" y="2.5" width="11" height="19" rx="2.8"/><path d="M10.5 18.5h3"/>',
  server:
    '<rect x="3.5" y="4" width="17" height="7" rx="2"/><rect x="3.5" y="13" width="17" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01" stroke-width="2.6"/>',
  paperplane: '<path d="M21 3 10.2 13.8"/><path d="M21 3 14.6 21l-4.4-7.2L3 9.4z"/>',
  doc: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 13h6M9 17h4"/>',
  gear: '<circle cx="12" cy="12" r="3.1"/><path d="M10.3 2.8h3.4l.5 2.4 1.9 1.1 2.3-.8 1.7 2.9-1.8 1.6v2.2l1.8 1.6-1.7 2.9-2.3-.8-1.9 1.1-.5 2.4h-3.4l-.5-2.4-1.9-1.1-2.3.8-1.7-2.9 1.8-1.6v-2.2L3.9 8.4l1.7-2.9 2.3.8 1.9-1.1z"/>',
  person: '<circle cx="12" cy="8" r="3.8"/><path d="M4.5 20.5a7.5 7.5 0 0 1 15 0"/>',
  people:
    '<circle cx="9" cy="8.5" r="3.3"/><path d="M2.8 19.5a6.2 6.2 0 0 1 12.4 0"/><path d="M15.5 5.6a3.2 3.2 0 0 1 0 6M17.6 14.3a6 6 0 0 1 3.6 5.2"/>',
  search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m20 20-4.8-4.8"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  minus: '<path d="M5 12h14"/>',
  "chev-right": '<path d="m9 5 7 7-7 7"/>',
  "chev-left": '<path d="m15 5-7 7 7 7"/>',
  "chev-down": '<path d="m5 9 7 7 7-7"/>',
  "chev-updown": '<path d="m8 9.5 4-4 4 4M8 14.5l4 4 4-4"/>',
  check: '<path d="m5 12.5 4.5 4.5L19 7.5"/>',
  x: '<path d="M6 6l12 12M18 6 6 18"/>',
  "check-circle": '<circle cx="12" cy="12" r="9"/><path d="m8 12.3 2.8 2.8 5.4-5.6"/>',
  "x-circle": '<circle cx="12" cy="12" r="9"/><path d="m9 9 6 6M15 9l-6 6"/>',
  alert:
    '<path d="M10.3 4.2 2.9 17.5a2 2 0 0 0 1.7 3h14.8a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0z"/><path d="M12 9.5v4M12 17h.01"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.8h.01"/>',
  question:
    '<circle cx="12" cy="12" r="9"/><path d="M9.6 9.3a2.5 2.5 0 0 1 4.8.9c0 1.7-2.4 2.2-2.4 3.8M12 17h.01"/>',
  "slash-circle": '<circle cx="12" cy="12" r="9"/><path d="m5.7 18.3 12.6-12.6"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>',
  hourglass:
    '<path d="M6.5 3h11M6.5 21h11M7.5 3v3.5a4.5 4.5 0 0 0 9 0V3M7.5 21v-3.5a4.5 4.5 0 0 1 9 0V21"/>',
  bolt: '<path d="M13 2.5 4.5 13.5H12l-1 8 8.5-11H12z"/>',
  plug: '<path d="M9 2.5v5M15 2.5v5"/><path d="M6.5 7.5h11v3a5.5 5.5 0 0 1-11 0z"/><path d="M12 16v5.5"/>',
  battery:
    '<rect x="2.5" y="7" width="17" height="10" rx="2.6"/><path d="M22 10.5v3"/><rect x="5" y="9.5" width="7.5" height="5" rx="1.1" fill="currentColor" stroke="none"/>',
  thermo: '<path d="M14 14.8V5a2 2 0 0 0-4 0v9.8a4 4 0 1 0 4 0z"/><path d="M12 15.5V9"/>',
  wave: '<path d="M2.5 12c1.6-4.5 3.2-4.5 4.8 0s3.2 4.5 4.8 0 3.2-4.5 4.8 0 2.4 3.4 4.6 1"/>',
  gauge:
    '<path d="M4.3 17.5a8.7 8.7 0 1 1 15.4 0"/><path d="m12 13.6 3.8-4.6"/><circle cx="12" cy="13.8" r="1.3"/>',
  antenna:
    '<path d="M12 12.5V21"/><circle cx="12" cy="10.3" r="1.6"/><path d="M8.3 6.6a5.2 5.2 0 0 0 0 7.4M15.7 6.6a5.2 5.2 0 0 1 0 7.4M5.5 3.8a9.2 9.2 0 0 0 0 13M18.5 3.8a9.2 9.2 0 0 1 0 13"/>',
  "wifi-off":
    '<path d="M2.5 8.6a14.5 14.5 0 0 1 19 0M5.8 12.1a9.6 9.6 0 0 1 12.4 0M9.1 15.6a4.8 4.8 0 0 1 5.8 0"/><path d="M12 19.3h.01" stroke-width="2.8"/><path d="M3.5 3.5l17 17"/>',
  refresh: '<path d="M20 11.5a8 8 0 1 1-2.4-5.7"/><path d="M20 4v5h-5"/>',
  ellipsis: '<path d="M5.5 12h.01M12 12h.01M18.5 12h.01" stroke-width="3"/>',
  lock: '<rect x="5" y="10.5" width="14" height="10" rx="2.2"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"/>',
  key: '<circle cx="8" cy="15.5" r="4.3"/><path d="m11.1 12.4 8.4-8.4M16.5 7l2.4 2.4M14.3 9.2l1.8 1.8"/>',
  shield:
    '<path d="M12 2.8 4.5 5.6v6c0 4.6 3.2 8.3 7.5 9.6 4.3-1.3 7.5-5 7.5-9.6v-6z"/><path d="m8.8 12 2.3 2.3 4.3-4.6"/>',
  terminal:
    '<rect x="2.5" y="4" width="19" height="16" rx="2.6"/><path d="m6.5 9 3 3-3 3M12 15h5"/>',
  globe:
    '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.6 3.8 5.6 3.8 9s-1.3 6.4-3.8 9c-2.5-2.6-3.8-5.6-3.8-9S9.5 5.6 12 3z"/>',
  trash:
    '<path d="M4.5 6.5h15M9.5 6.5v-2h5v2M6.5 6.5l1 13a1.5 1.5 0 0 0 1.5 1.4h6a1.5 1.5 0 0 0 1.5-1.4l1-13"/>',
  play: '<path d="M7.5 4.8v14.4a.8.8 0 0 0 1.2.7l11.3-7.2a.8.8 0 0 0 0-1.4L8.7 4.1a.8.8 0 0 0-1.2.7z"/>',
  pause: '<path d="M8.5 5v14M15.5 5v14"/>',
  calendar:
    '<rect x="3.5" y="5" width="17" height="15.5" rx="2.5"/><path d="M3.5 10h17M8 3v4M16 3v4"/>',
  chart: '<path d="M3.5 20.5h17"/><path d="m4.5 15.5 4.5-5 4 3.5 6.5-8"/>',
  sliders:
    '<path d="M4 6.5h9M17 6.5h3M4 17.5h3M11 17.5h9"/><circle cx="15" cy="6.5" r="2"/><circle cx="9" cy="17.5" r="2"/>',
  copy: '<rect x="8.5" y="8.5" width="12" height="12" rx="2.2"/><path d="M15.5 8.5V5.7a2.2 2.2 0 0 0-2.2-2.2H5.7a2.2 2.2 0 0 0-2.2 2.2v7.6a2.2 2.2 0 0 0 2.2 2.2h2.8"/>',
  share:
    '<path d="M12 3v12M7.5 7.5 12 3l4.5 4.5"/><path d="M8 10.5H6.5a2 2 0 0 0-2 2v6.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-6.5a2 2 0 0 0-2-2H16"/>',
  download:
    '<path d="M12 3.5v11M7.5 10l4.5 4.5 4.5-4.5"/><path d="M4.5 16v2.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V16"/>',
  moon: '<path d="M20 14.5A8.5 8.5 0 1 1 9.5 4a7 7 0 0 0 10.5 10.5z"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M4.6 4.6 6 6M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4 6 18M18 6l1.4-1.4"/>',
  list: '<path d="M9 6h11M9 12h11M9 18h11"/><path d="M4.5 6h.01M4.5 12h.01M4.5 18h.01" stroke-width="2.8"/>',
  eye: '<path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z"/><circle cx="12" cy="12" r="3"/>',
  flask:
    '<path d="M9.5 3h5M10.5 3v6L5 18.5A1.7 1.7 0 0 0 6.5 21h11a1.7 1.7 0 0 0 1.5-2.5L13.5 9V3"/><path d="M7.4 14.8h9.2"/>',
  tag: '<path d="M3.5 12.6V4.5a1 1 0 0 1 1-1h8.1a1 1 0 0 1 .7.3l7.4 7.4a1 1 0 0 1 0 1.4l-8.1 8.1a1 1 0 0 1-1.4 0l-7.4-7.4a1 1 0 0 1-.3-.7z"/><circle cx="8" cy="8" r="1.4"/>',
  pin: '<path d="M12 21s-6.5-6-6.5-11a6.5 6.5 0 0 1 13 0c0 5-6.5 11-6.5 11z"/><circle cx="12" cy="10" r="2.3"/>',
  fingerprint:
    '<path d="M12 11v3.5c0 2.5-.8 4.6-2.2 6.3"/><path d="M8.5 9.3a4 4 0 0 1 7 2.7v2.5c0 1.6-.3 3.2-.9 4.6"/><path d="M5.5 15.5c.3-1 .5-2 .5-3V12a6 6 0 0 1 9.6-4.8"/><path d="M18 9.7c.3.7.5 1.5.5 2.3v2.5"/>',
  "arrow-right": '<path d="M5 12h14M13 6l6 6-6 6"/>',
  "arrow-up": '<path d="M12 19V5M6 11l6-6 6 6"/>',
  undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11"/>',
  grip: '<path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01" stroke-width="2.8"/>',
  stack: '<path d="m12 3 9 4.5-9 4.5-9-4.5z"/><path d="m3 12 9 4.5 9-4.5M3 16.5 12 21l9-4.5"/>',
  pencil: '<path d="M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>',
  filter: '<path d="M3.5 5h17l-6.5 8v6l-4 2v-8z"/>',
} as const;

export type IconName = keyof typeof ICON_PATHS;
