/*
 * UPS Warden 效果图共享脚本。
 *
 * 只服务于 docs/design 下的静态效果图：注入 SVG 图标、渲染侧栏/标签栏、切换评审状态与主题，
 * 以及分段控件、开关、浮层、倒计时等轻交互。它不是前端实现，也不调用任何后端 API。
 *
 * URL 参数：
 *   ?state=<key>   预选评审状态（body[data-states] 中声明的 key）
 *   ?theme=dark    强制深色；?theme=light 强制浅色；省略则跟随系统
 *   ?embed=1       画廊缩略图模式，隐藏评审浮条
 */
(function () {
  "use strict";

  const params = new URLSearchParams(location.search);
  const root = document.documentElement;

  // 主题需在首帧前确定，避免浅色 → 深色闪烁。
  const forcedTheme = params.get("theme");
  if (forcedTheme === "dark" || forcedTheme === "light") root.dataset.theme = forcedTheme;
  if (params.get("embed") === "1") root.classList.add("is-embedded");

  /* ---------- 图标：24×24 描边风格，参照 SF Symbols 的比例与圆角 ---------- */
  const ICONS = {
    grid: '<rect x="3.5" y="3.5" width="7" height="7" rx="1.8"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.8"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.8"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.8"/>',
    ups: '<rect x="6" y="2.5" width="12" height="19" rx="2.6"/><path d="M9.5 6.8h5M9.5 10h5"/><path d="m12.6 13.2-1.9 2.8h2.6l-1.9 2.8"/>',
    bell: '<path d="M6 16.5V11a6 6 0 0 1 12 0v5.5l1.5 2h-15z"/><path d="M10 20.8a2.2 2.2 0 0 0 4 0"/>',
    branch: '<path d="M7 20.5V4"/><path d="m4 7 3-3 3 3"/><path d="M7 9.5a5.5 5.5 0 0 0 5.5 5.5H18"/><path d="m15.5 12.5 2.5 2.5-2.5 2.5"/>',
    power: '<path d="M12 3v8.5"/><path d="M7.05 6.3a7.2 7.2 0 1 0 9.9 0"/>',
    desktop: '<rect x="3" y="4" width="18" height="12.5" rx="2.2"/><path d="M9 20.5h6M12 16.5v4"/>',
    phone: '<rect x="6.5" y="2.5" width="11" height="19" rx="2.8"/><path d="M10.5 18.5h3"/>',
    server: '<rect x="3.5" y="4" width="17" height="7" rx="2"/><rect x="3.5" y="13" width="17" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01" stroke-width="2.6"/>',
    paperplane: '<path d="M21 3 10.2 13.8"/><path d="M21 3 14.6 21l-4.4-7.2L3 9.4z"/>',
    doc: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 13h6M9 17h4"/>',
    gear: '<circle cx="12" cy="12" r="3.1"/><path d="M10.3 2.8h3.4l.5 2.4 1.9 1.1 2.3-.8 1.7 2.9-1.8 1.6v2.2l1.8 1.6-1.7 2.9-2.3-.8-1.9 1.1-.5 2.4h-3.4l-.5-2.4-1.9-1.1-2.3.8-1.7-2.9 1.8-1.6v-2.2L3.9 8.4l1.7-2.9 2.3.8 1.9-1.1z"/>',
    person: '<circle cx="12" cy="8" r="3.8"/><path d="M4.5 20.5a7.5 7.5 0 0 1 15 0"/>',
    people: '<circle cx="9" cy="8.5" r="3.3"/><path d="M2.8 19.5a6.2 6.2 0 0 1 12.4 0"/><path d="M15.5 5.6a3.2 3.2 0 0 1 0 6M17.6 14.3a6 6 0 0 1 3.6 5.2"/>',
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
    alert: '<path d="M10.3 4.2 2.9 17.5a2 2 0 0 0 1.7 3h14.8a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0z"/><path d="M12 9.5v4M12 17h.01"/>',
    info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.8h.01"/>',
    question: '<circle cx="12" cy="12" r="9"/><path d="M9.6 9.3a2.5 2.5 0 0 1 4.8.9c0 1.7-2.4 2.2-2.4 3.8M12 17h.01"/>',
    "slash-circle": '<circle cx="12" cy="12" r="9"/><path d="m5.7 18.3 12.6-12.6"/>',
    clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>',
    hourglass: '<path d="M6.5 3h11M6.5 21h11M7.5 3v3.5a4.5 4.5 0 0 0 9 0V3M7.5 21v-3.5a4.5 4.5 0 0 1 9 0V21"/>',
    bolt: '<path d="M13 2.5 4.5 13.5H12l-1 8 8.5-11H12z"/>',
    plug: '<path d="M9 2.5v5M15 2.5v5"/><path d="M6.5 7.5h11v3a5.5 5.5 0 0 1-11 0z"/><path d="M12 16v5.5"/>',
    battery: '<rect x="2.5" y="7" width="17" height="10" rx="2.6"/><path d="M22 10.5v3"/><rect x="5" y="9.5" width="7.5" height="5" rx="1.1" fill="currentColor" stroke="none"/>',
    thermo: '<path d="M14 14.8V5a2 2 0 0 0-4 0v9.8a4 4 0 1 0 4 0z"/><path d="M12 15.5V9"/>',
    wave: '<path d="M2.5 12c1.6-4.5 3.2-4.5 4.8 0s3.2 4.5 4.8 0 3.2-4.5 4.8 0 2.4 3.4 4.6 1"/>',
    gauge: '<path d="M4.3 17.5a8.7 8.7 0 1 1 15.4 0"/><path d="m12 13.6 3.8-4.6"/><circle cx="12" cy="13.8" r="1.3"/>',
    antenna: '<path d="M12 12.5V21"/><circle cx="12" cy="10.3" r="1.6"/><path d="M8.3 6.6a5.2 5.2 0 0 0 0 7.4M15.7 6.6a5.2 5.2 0 0 1 0 7.4M5.5 3.8a9.2 9.2 0 0 0 0 13M18.5 3.8a9.2 9.2 0 0 1 0 13"/>',
    "wifi-off": '<path d="M2.5 8.6a14.5 14.5 0 0 1 19 0M5.8 12.1a9.6 9.6 0 0 1 12.4 0M9.1 15.6a4.8 4.8 0 0 1 5.8 0"/><path d="M12 19.3h.01" stroke-width="2.8"/><path d="M3.5 3.5l17 17"/>',
    refresh: '<path d="M20 11.5a8 8 0 1 1-2.4-5.7"/><path d="M20 4v5h-5"/>',
    ellipsis: '<path d="M5.5 12h.01M12 12h.01M18.5 12h.01" stroke-width="3"/>',
    lock: '<rect x="5" y="10.5" width="14" height="10" rx="2.2"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"/>',
    key: '<circle cx="8" cy="15.5" r="4.3"/><path d="m11.1 12.4 8.4-8.4M16.5 7l2.4 2.4M14.3 9.2l1.8 1.8"/>',
    shield: '<path d="M12 2.8 4.5 5.6v6c0 4.6 3.2 8.3 7.5 9.6 4.3-1.3 7.5-5 7.5-9.6v-6z"/><path d="m8.8 12 2.3 2.3 4.3-4.6"/>',
    terminal: '<rect x="2.5" y="4" width="19" height="16" rx="2.6"/><path d="m6.5 9 3 3-3 3M12 15h5"/>',
    globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.6 3.8 5.6 3.8 9s-1.3 6.4-3.8 9c-2.5-2.6-3.8-5.6-3.8-9S9.5 5.6 12 3z"/>',
    trash: '<path d="M4.5 6.5h15M9.5 6.5v-2h5v2M6.5 6.5l1 13a1.5 1.5 0 0 0 1.5 1.4h6a1.5 1.5 0 0 0 1.5-1.4l1-13"/>',
    play: '<path d="M7.5 4.8v14.4a.8.8 0 0 0 1.2.7l11.3-7.2a.8.8 0 0 0 0-1.4L8.7 4.1a.8.8 0 0 0-1.2.7z"/>',
    pause: '<path d="M8.5 5v14M15.5 5v14"/>',
    calendar: '<rect x="3.5" y="5" width="17" height="15.5" rx="2.5"/><path d="M3.5 10h17M8 3v4M16 3v4"/>',
    chart: '<path d="M3.5 20.5h17"/><path d="m4.5 15.5 4.5-5 4 3.5 6.5-8"/>',
    sliders: '<path d="M4 6.5h9M17 6.5h3M4 17.5h3M11 17.5h9"/><circle cx="15" cy="6.5" r="2"/><circle cx="9" cy="17.5" r="2"/>',
    copy: '<rect x="8.5" y="8.5" width="12" height="12" rx="2.2"/><path d="M15.5 8.5V5.7a2.2 2.2 0 0 0-2.2-2.2H5.7a2.2 2.2 0 0 0-2.2 2.2v7.6a2.2 2.2 0 0 0 2.2 2.2h2.8"/>',
    share: '<path d="M12 3v12M7.5 7.5 12 3l4.5 4.5"/><path d="M8 10.5H6.5a2 2 0 0 0-2 2v6.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2v-6.5a2 2 0 0 0-2-2H16"/>',
    download: '<path d="M12 3.5v11M7.5 10l4.5 4.5 4.5-4.5"/><path d="M4.5 16v2.5a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V16"/>',
    moon: '<path d="M20 14.5A8.5 8.5 0 1 1 9.5 4a7 7 0 0 0 10.5 10.5z"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M4.6 4.6 6 6M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4 6 18M18 6l1.4-1.4"/>',
    list: '<path d="M9 6h11M9 12h11M9 18h11"/><path d="M4.5 6h.01M4.5 12h.01M4.5 18h.01" stroke-width="2.8"/>',
    eye: '<path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12z"/><circle cx="12" cy="12" r="3"/>',
    flask: '<path d="M9.5 3h5M10.5 3v6L5 18.5A1.7 1.7 0 0 0 6.5 21h11a1.7 1.7 0 0 0 1.5-2.5L13.5 9V3"/><path d="M7.4 14.8h9.2"/>',
    tag: '<path d="M3.5 12.6V4.5a1 1 0 0 1 1-1h8.1a1 1 0 0 1 .7.3l7.4 7.4a1 1 0 0 1 0 1.4l-8.1 8.1a1 1 0 0 1-1.4 0l-7.4-7.4a1 1 0 0 1-.3-.7z"/><circle cx="8" cy="8" r="1.4"/>',
    pin: '<path d="M12 21s-6.5-6-6.5-11a6.5 6.5 0 0 1 13 0c0 5-6.5 11-6.5 11z"/><circle cx="12" cy="10" r="2.3"/>',
    fingerprint: '<path d="M12 11v3.5c0 2.5-.8 4.6-2.2 6.3"/><path d="M8.5 9.3a4 4 0 0 1 7 2.7v2.5c0 1.6-.3 3.2-.9 4.6"/><path d="M5.5 15.5c.3-1 .5-2 .5-3V12a6 6 0 0 1 9.6-4.8"/><path d="M18 9.7c.3.7.5 1.5.5 2.3v2.5"/>',
    "arrow-right": '<path d="M5 12h14M13 6l6 6-6 6"/>',
    "arrow-up": '<path d="M12 19V5M6 11l6-6 6 6"/>',
    undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11"/>',
    grip: '<path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01" stroke-width="2.8"/>',
    stack: '<path d="m12 3 9 4.5-9 4.5-9-4.5z"/><path d="m3 12 9 4.5 9-4.5M3 16.5 12 21l9-4.5"/>',
    pencil: '<path d="M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>',
    filter: '<path d="M3.5 5h17l-6.5 8v6l-4 2v-8z"/>',
  };

  function iconSprite() {
    const symbols = Object.entries(ICONS)
      .map(([id, body]) => `<symbol id="i-${id}" viewBox="0 0 24 24">${body}</symbol>`)
      .join("");
    const holder = document.createElement("div");
    holder.hidden = true;
    holder.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg">${symbols}</svg>`;
    document.body.prepend(holder);
  }

  const icon = (id, cls = "i") => `<svg class="${cls}" aria-hidden="true"><use href="#i-${id}"/></svg>`;

  /* ---------- 导航：名称直接描述内容，分组反映任务（监控 → 自动化 → 通知 → 系统） ---------- */
  const NAV = [
    {
      group: "监控",
      items: [
        { id: "overview", label: "总览", href: "overview.html", icon: "grid", color: "var(--accent)" },
        { id: "devices", label: "设备", href: "devices.html", icon: "ups", color: "var(--green)" },
        { id: "alerts", label: "告警与事件", href: "alerts.html", icon: "bell", color: "var(--red)", badge: "4" },
      ],
    },
    {
      group: "自动化",
      items: [
        { id: "rules", label: "规则", href: "rules.html", icon: "branch", color: "var(--indigo)" },
        { id: "plans", label: "关机计划", href: "shutdown-plans.html", icon: "power", color: "var(--orange)" },
        { id: "hosts", label: "主机与动作", href: "hosts-actions.html", icon: "desktop", color: "var(--teal)" },
      ],
    },
    {
      group: "通知",
      items: [{ id: "notifications", label: "通知渠道", href: "notifications.html", icon: "paperplane", color: "var(--purple)" }],
    },
    {
      group: "系统",
      items: [
        { id: "profiles", label: "Profile", href: null, icon: "doc", color: "var(--brown)", note: "本轮未出图" },
        { id: "settings", label: "设置", href: "settings.html", icon: "gear", color: "var(--gray)" },
      ],
    },
  ];
  const TABS = ["overview", "alerts", "devices", "plans", "settings"];

  function renderSidebar(page) {
    const aside = document.querySelector(".sidebar");
    if (!aside || aside.children.length) return;
    const groups = NAV.map(
      (g) => `<div class="nav-group"><div class="nav-label">${g.group}</div>${g.items
        .map((it) => {
          const current = it.id === page ? ' aria-current="page"' : "";
          const attrs = it.href ? `href="${it.href}"` : `aria-disabled="true" title="${it.note}"`;
          const badge = it.badge ? `<span class="nav-badge">${it.badge}</span>` : "";
          return `<a class="nav-item" ${attrs}${current}><span class="nav-ico" style="--c:${it.color}">${icon(it.icon)}</span>${it.label}${badge}</a>`;
        })
        .join("")}</div>`,
    ).join("");
    aside.innerHTML = `
      <div class="brand">
        <span class="app-icon">${icon("bolt")}</span>
        <div><div class="brand-name">UPS Warden</div><div class="brand-sub">机房与办公区 · 6 台设备</div></div>
      </div>
      <div class="side-search">${icon("search")}<span>搜索设备、规则、主机</span><kbd>⌘K</kbd></div>
      <nav aria-label="主导航">${groups}</nav>
      <div class="side-foot">
        <div class="live-status" data-when-not="offline"><span class="live-dot"></span>实时连接正常 · 已同步</div>
        <div class="live-status" data-when="offline"><span class="live-dot is-warn breathe"></span>实时连接已断开，正在重连…</div>
        <div class="user-row"><span class="avatar">AD</span><div style="flex:1;min-width:0"><div class="t-subhead strong">admin</div><div class="t-caption faint">管理员 · 会话至 10月28日 14:05</div></div>${icon("chev-updown", "i faint")}</div>
      </div>`;
  }

  function renderTabbar(page) {
    const app = document.querySelector(".app");
    if (!app || document.querySelector(".tabbar")) return;
    const all = NAV.flatMap((g) => g.items);
    const nav = document.createElement("nav");
    nav.className = "tabbar";
    nav.setAttribute("aria-label", "标签栏");
    nav.innerHTML = TABS.map((id) => {
      const it = all.find((x) => x.id === id);
      const current = it.id === page ? ' aria-current="page"' : "";
      const badge = it.badge ? `<span class="nav-badge">${it.badge}</span>` : "";
      const label = it.id === "alerts" ? "告警" : it.id === "plans" ? "计划" : it.label;
      return `<a class="tab" href="${it.href}"${current}>${icon(it.icon)}${badge}${label}</a>`;
    }).join("");
    app.after(nav);
  }

  /* ---------- 评审状态：同一页面的多个业务状态（例如「正常 / 停电中」） ---------- */
  function parseStates() {
    const raw = document.body.dataset.states;
    if (!raw) return [];
    return raw.split(",").map((pair) => {
      const [key, label] = pair.split(":");
      return { key: key.trim(), label: (label || key).trim() };
    });
  }

  function applyState(key) {
    document.body.dataset.state = key;
    document.querySelectorAll("[data-when]").forEach((el) => {
      el.hidden = !el.dataset.when.split(/\s+/).includes(key);
    });
    document.querySelectorAll("[data-when-not]").forEach((el) => {
      el.hidden = el.dataset.whenNot.split(/\s+/).includes(key);
    });
    // 页内控件（分段控件、向导「下一步」等）用 data-goto 切换评审状态；data-match 列出同样应高亮的状态。
    const matches = (el) => [el.dataset.goto, ...(el.dataset.match || "").split(/\s+/)].includes(key);
    document.querySelectorAll(".seg [data-goto]").forEach((b) => b.setAttribute("aria-pressed", String(matches(b))));
    document.querySelectorAll(".row[data-goto]").forEach((r) => r.classList.toggle("is-selected", matches(r)));
    document.querySelectorAll(".mock-bar [data-state]").forEach((b) => {
      b.setAttribute("aria-pressed", String(b.dataset.state === key));
    });
    // 状态切换可能让分段控件从隐藏变为可见，需要重新测量滑块。
    requestAnimationFrame(() => document.querySelectorAll(".seg").forEach((s) => syncSeg(s, true)));
  }

  function renderMockBar(states) {
    const bar = document.createElement("div");
    bar.className = "mock-bar";
    bar.setAttribute("role", "region");
    bar.setAttribute("aria-label", "效果图评审控制（非产品界面）");
    const current = document.body.dataset.state;
    const seg = states.length > 1
      ? `<div class="seg" data-role="state">${states
          .map((s) => `<button type="button" data-state="${s.key}" aria-pressed="${s.key === current}">${s.label}</button>`)
          .join("")}</div>`
      : "";
    bar.innerHTML = `<span class="mock-label">${states.length > 1 ? "效果图状态" : "外观"}</span>${seg}
      <button type="button" class="btn btn-gray btn-icon btn-sm" data-role="theme" aria-label="切换浅色/深色">${icon("moon")}</button>`;
    document.body.append(bar);
    bar.addEventListener("click", (e) => {
      const stateBtn = e.target.closest("[data-state]");
      if (stateBtn) applyState(stateBtn.dataset.state);
      if (e.target.closest('[data-role="theme"]')) {
        const dark = getComputedStyle(root).colorScheme.includes("dark");
        root.dataset.theme = dark ? "light" : "dark";
      }
    });
  }

  /* ---------- 分段控件：滑块从当前位置滑向目标，不跳变 ---------- */
  function syncSeg(seg, animate) {
    const active = seg.querySelector('button[aria-pressed="true"]');
    if (!active || !seg.offsetParent) return;
    // 首次测量（刚变为可见）没有「当前位置」可滑动，直接落位，避免滑块从左缘飞入。
    const instant = !animate || !seg.style.getPropertyValue("--seg-w");
    if (instant) seg.classList.add("no-anim");
    seg.style.setProperty("--seg-x", `${active.offsetLeft}px`);
    seg.style.setProperty("--seg-w", `${active.offsetWidth}px`);
    if (instant) requestAnimationFrame(() => seg.classList.remove("no-anim"));
  }

  function bindSegments() {
    document.addEventListener("click", (e) => {
      const btn = e.target.closest(".seg button");
      if (!btn) return;
      const seg = btn.parentElement;
      seg.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b === btn)));
      syncSeg(seg, true);
    });
    document.querySelectorAll(".seg").forEach((s) => syncSeg(s, false));
    addEventListener("resize", () => document.querySelectorAll(".seg").forEach((s) => syncSeg(s, false)));
  }

  /* ---------- 页内标签：data-tab="组:值" 触发，data-panel="组:值" 面板 ---------- */
  function bindTabs() {
    document.addEventListener("click", (e) => {
      const go = e.target.closest("[data-goto]");
      if (go) {
        applyState(go.dataset.goto);
        return;
      }
      const trigger = e.target.closest("[data-tab]");
      if (!trigger) return;
      const [group, value] = trigger.dataset.tab.split(":");
      document.querySelectorAll(`[data-tab^="${group}:"]`).forEach((t) => {
        const on = t.dataset.tab === `${group}:${value}`;
        if (t.matches(".seg button")) t.setAttribute("aria-pressed", String(on));
        else t.classList.toggle("is-selected", on);
      });
      document.querySelectorAll(`[data-panel^="${group}:"]`).forEach((p) => {
        p.hidden = p.dataset.panel !== `${group}:${value}`;
      });
      requestAnimationFrame(() => document.querySelectorAll(".seg").forEach((s) => syncSeg(s, false)));
    });
  }

  /* ---------- 开关与浮层；带 data-guard 的开关关闭前必须经确认对话框 ---------- */
  let pendingSwitch = null;
  function bindSwitchesAndOverlays() {
    document.addEventListener("click", (e) => {
      const sw = e.target.closest(".switch");
      if (sw) {
        const on = sw.getAttribute("aria-checked") === "true";
        if (on && sw.dataset.guard) {
          pendingSwitch = sw;
          const dlg = document.getElementById(sw.dataset.guard);
          if (dlg) dlg.hidden = false;
          return;
        }
        sw.setAttribute("aria-checked", String(!on));
        return;
      }
      const opener = e.target.closest("[data-open]");
      if (opener) {
        const target = document.getElementById(opener.dataset.open);
        if (target) target.hidden = false;
        return;
      }
      if (e.target.closest("[data-guard-confirm]") && pendingSwitch) {
        pendingSwitch.setAttribute("aria-checked", "false");
        pendingSwitch = null;
      }
      const closer = e.target.closest("[data-close]");
      if (closer) {
        const overlay = closer.closest(".overlay");
        if (overlay) overlay.hidden = true;
      }
    });
    document.addEventListener("keydown", (e) => {
      if (e.key !== "Escape") return;
      document.querySelectorAll(".overlay:not([hidden])").forEach((o) => {
        o.hidden = true;
      });
    });
  }

  /* ---------- 输入计划名称后才允许执行（FR-UI-06） ---------- */
  function bindConfirmName() {
    document.querySelectorAll("input[data-confirm-name]").forEach((input) => {
      const btn = document.querySelector(input.dataset.confirmTarget);
      if (!btn) return;
      const check = () => {
        const ok = input.value === input.dataset.confirmName;
        btn.toggleAttribute("disabled", !ok);
        input.classList.toggle("is-invalid", input.value.length > 0 && !ok);
      };
      input.addEventListener("input", check);
      check();
    });
  }

  /* ---------- 计时：倒计时按 countdown_until 推算，放电计时按 on_battery_since 推算 ---------- */
  const mmss = (s) => `${String(Math.floor(s / 60)).padStart(2, "0")}:${String(s % 60).padStart(2, "0")}`;
  function bindTimers() {
    const countdowns = [...document.querySelectorAll("[data-countdown]")].map((el) => ({
      el,
      left: Number(el.dataset.countdown),
      total: Number(el.dataset.total || 60),
    }));
    const elapsed = [...document.querySelectorAll("[data-elapsed]")].map((el) => ({ el, s: Number(el.dataset.elapsed) }));
    const paint = () => {
      countdowns.forEach((c) => {
        c.el.querySelectorAll(".cd-text").forEach((t) => {
          t.textContent = mmss(c.left);
        });
        const bar = c.el.querySelector(".ring .bar");
        if (bar) bar.setAttribute("stroke-dasharray", `${((c.left / c.total) * 100).toFixed(2)} 100`);
      });
      elapsed.forEach((x) => {
        x.el.textContent = mmss(x.s);
      });
    };
    paint();
    setInterval(() => {
      countdowns.forEach((c) => {
        if (c.left > 0) c.left -= 1;
      });
      elapsed.forEach((x) => {
        x.s += 1;
      });
      paint();
    }, 1000);
  }

  /* ---------- 工具栏：内容滚到其下方时才显示边缘效果与小标题 ---------- */
  function bindToolbar() {
    const bar = document.querySelector(".toolbar");
    if (!bar) return;
    const update = () => bar.classList.toggle("is-scrolled", scrollY > 24);
    addEventListener("scroll", update, { passive: true });
    update();
  }

  function init() {
    iconSprite();
    const page = document.body.dataset.page;
    renderSidebar(page);
    renderTabbar(page);
    const states = parseStates();
    const requested = params.get("state");
    const initial = states.find((s) => s.key === requested) || states[0];
    // 未声明评审状态的页面也执行一次，保证 data-when / data-when-not 元素处于默认可见性。
    applyState(initial ? initial.key : "default");
    renderMockBar(states);
    bindSegments();
    bindTabs();
    bindSwitchesAndOverlays();
    bindConfirmName();
    bindTimers();
    bindToolbar();
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
