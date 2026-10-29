import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { Icon } from "../icons/Icon.tsx";
import { LiveBanner } from "./LiveBanner.tsx";

/** 内容滚动超过阈值后工具栏显示边缘效果与小标题（apple-ui.js bindToolbar）。 */
function useScrolled(threshold = 24): boolean {
  const [scrolled, setScrolled] = useState(
    () => typeof window !== "undefined" && window.scrollY > threshold,
  );
  useEffect(() => {
    const update = () => setScrolled(window.scrollY > threshold);
    update();
    window.addEventListener("scroll", update, { passive: true });
    return () => window.removeEventListener("scroll", update);
  }, [threshold]);
  return scrolled;
}

export interface PageProps {
  title: string;
  /** 标题上方的小字（日期、上级分类等）。 */
  eyebrow?: ReactNode;
  /** 标题下方说明。 */
  subtitle?: ReactNode;
  /** 页头右侧（如“14:21:15 更新”）。 */
  headAside?: ReactNode;
  /** 工具栏右侧的图标按钮。 */
  toolbar?: ReactNode;
  /** 工具栏左侧返回链接（详情页）。 */
  back?: { to: string; label: string };
  /** 取消内容最大宽度（宽表格页面）。 */
  wide?: boolean;
  /** 是否显示实时连接断开横幅，默认显示。 */
  liveBanner?: boolean;
  children?: ReactNode;
}

/** 页面骨架：工具栏 → 页头 → 内容；所有业务页面共用。 */
export function Page({
  title,
  eyebrow,
  subtitle,
  headAside,
  toolbar,
  back,
  wide,
  liveBanner = true,
  children,
}: PageProps) {
  const scrolled = useScrolled();
  useEffect(() => {
    document.title = `${title} · UPS Warden`;
  }, [title]);

  return (
    <>
      <header className={scrolled ? "toolbar is-scrolled" : "toolbar"}>
        {back ? (
          <Link className="back" to={back.to}>
            <Icon name="chev-left" />
            {back.label}
          </Link>
        ) : null}
        {/* 与页头 h1 重复，只在滚动后作视觉提示，读屏忽略。 */}
        <span className="toolbar-title" aria-hidden="true">
          {title}
        </span>
        <div className="spacer" />
        {toolbar}
      </header>
      <div className={wide ? "page-head is-wide" : "page-head"}>
        <div>
          {eyebrow ? <div className="eyebrow">{eyebrow}</div> : null}
          <h1 className="page-title">{title}</h1>
          {subtitle ? <p className="page-sub">{subtitle}</p> : null}
        </div>
        {headAside ? <div className="hstack hide-narrow">{headAside}</div> : null}
      </div>
      <div className={wide ? "content content-wide" : "content"}>
        {liveBanner ? <LiveBanner /> : null}
        {children}
      </div>
    </>
  );
}
