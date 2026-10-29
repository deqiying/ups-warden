import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vite-plus/test";
import { Badge } from "./Badge.tsx";
import { Banner } from "./Banner.tsx";
import { Empty } from "./Empty.tsx";
import { FormRow } from "./FormRow.tsx";
import { Input, Textarea } from "./Input.tsx";
import { List } from "./List.tsx";
import { Pill } from "./Pill.tsx";
import { Row } from "./Row.tsx";
import { SEVERITY_LABELS, Severity } from "./Severity.tsx";
import { Spinner } from "./Spinner.tsx";
import { Table } from "./Table.tsx";

describe("Pill / Badge", () => {
  it("gray 不加修饰类，其余按色调", () => {
    const { container, rerender } = render(<Pill>离线</Pill>);
    expect(container.firstElementChild?.className).toBe("pill");
    rerender(
      <Pill tone="green" size="sm" dot breathe>
        在线
      </Pill>,
    );
    expect(container.firstElementChild?.className).toBe("pill pill-green pill-sm");
    expect(container.querySelector(".dot")?.className).toBe("dot breathe");
  });

  it("Badge 色调与图标", () => {
    const { container, rerender } = render(<Badge>SNMP</Badge>);
    expect(container.firstElementChild?.className).toBe("badge");
    rerender(
      <Badge tone="orange" icon="flask">
        dry-run
      </Badge>,
    );
    expect(container.firstElementChild?.className).toBe("badge badge-orange");
    expect(container.querySelector("svg.i")).not.toBeNull();
  });
});

describe("Severity", () => {
  it.each([
    ["info", "信息", "#i-info"],
    ["warning", "警告", "#i-alert"],
    ["critical", "严重", "#i-alert"],
    ["ok", "正常", "#i-check"],
  ] as const)("level=%s 使用图标与可读名称", (level, label, href) => {
    const { container } = render(<Severity level={level} />);
    expect(container.firstElementChild?.className).toBe(`sev sev-${level}`);
    expect(screen.getByRole("img", { name: label })).toBeTruthy();
    expect(container.querySelector("use")?.getAttribute("href")).toBe(href);
    expect(SEVERITY_LABELS[level]).toBe(label);
  });

  it("label 可覆盖", () => {
    render(<Severity level="critical" label="市电中断" />);
    expect(screen.getByRole("img", { name: "市电中断" })).toBeTruthy();
  });
});

describe("Input / Textarea", () => {
  it("invalid 加 is-invalid 并设置 aria-invalid，支持 size 与 mono", () => {
    render(<Input aria-label="名称" size="lg" mono invalid />);
    const input = screen.getByRole("textbox", { name: "名称" });
    expect(input.className).toBe("input input-lg input-mono is-invalid");
    expect(input.getAttribute("aria-invalid")).toBe("true");
  });

  it("未 invalid 时不设置 aria-invalid；ref 可用", () => {
    let node: HTMLInputElement | null = null;
    render(
      <Input
        aria-label="名称"
        ref={(el) => {
          node = el;
        }}
      />,
    );
    expect(node).toBe(screen.getByRole("textbox"));
    expect(screen.getByRole("textbox").getAttribute("aria-invalid")).toBeNull();
  });

  it("Textarea 类名", () => {
    render(<Textarea aria-label="模板" mono invalid />);
    const area = screen.getByRole("textbox", { name: "模板" });
    expect(area.className).toBe("textarea input-mono is-invalid");
    expect(area.getAttribute("aria-invalid")).toBe("true");
  });
});

describe("Banner", () => {
  it("info 无修饰类并使用默认图标；其余色调加 banner-<tone>", () => {
    const { container, rerender } = render(<Banner title="提示">正文</Banner>);
    expect(container.firstElementChild?.className).toBe("banner");
    expect(container.querySelector("use")?.getAttribute("href")).toBe("#i-info");
    expect(container.querySelector(".banner-title")?.textContent).toBe("提示");
    expect(container.querySelector(".banner-text")?.textContent).toBe("正文");

    rerender(<Banner tone="critical" role="alert" actions={<button type="button">重试</button>} />);
    expect(container.firstElementChild?.className).toBe("banner banner-critical");
    expect(container.querySelector("use")?.getAttribute("href")).toBe("#i-x-circle");
    expect(screen.getByRole("alert")).toBeTruthy();
    expect(container.querySelector(".actions button")).not.toBeNull();
    expect(container.querySelector("svg.banner-icon")).not.toBeNull();
  });

  it("indigo 默认 flask 图标，可用 icon 覆盖", () => {
    const { container, rerender } = render(<Banner tone="indigo">dry-run</Banner>);
    expect(container.querySelector("use")?.getAttribute("href")).toBe("#i-flask");
    rerender(
      <Banner tone="warning" icon="antenna">
        通讯中断
      </Banner>,
    );
    expect(container.querySelector("use")?.getAttribute("href")).toBe("#i-antenna");
  });
});

describe("List / Row / FormRow", () => {
  it("List 修饰类与 --row-inset", () => {
    const { container } = render(<List hasIcons flat inset={20} aria-label="设备" role="list" />);
    const list = container.firstElementChild as HTMLElement;
    expect(list.className).toBe("list has-icons is-flat");
    expect(list.style.getPropertyValue("--row-inset")).toBe("20px");
    expect(list.getAttribute("aria-label")).toBe("设备");
  });

  it("Row 渲染图标、标题、值与箭头，iconColor 写入 --c", () => {
    const { container } = render(
      <Row
        icon="clock"
        iconColor="var(--orange)"
        softIcon
        title="倒计时"
        sub="说明"
        value="60 秒"
        chevron
      >
        <span data-testid="extra" />
      </Row>,
    );
    const icon = container.querySelector(".row-icon") as HTMLElement;
    expect(icon.className).toBe("row-icon is-soft");
    expect(icon.style.getPropertyValue("--c")).toBe("var(--orange)");
    expect(container.querySelector(".row-title")?.textContent).toBe("倒计时");
    expect(container.querySelector(".row-sub")?.textContent).toBe("说明");
    expect(container.querySelector(".row-value")?.textContent).toBe("60 秒");
    expect(container.querySelector(".row-chevron")).not.toBeNull();
    // children 位于 row-main 之后、row-value 之前
    const order = Array.from(container.firstElementChild?.children ?? []).map(
      (el) => el.getAttribute("data-testid") ?? el.className.split(" ")[0],
    );
    expect(order.indexOf("row-main")).toBeLessThan(order.indexOf("extra"));
    expect(order.indexOf("extra")).toBeLessThan(order.indexOf("row-value"));
  });

  it("onActivate 使行可点击且可键盘触发；selected 设置 aria-current", async () => {
    const user = userEvent.setup();
    const onActivate = vi.fn();
    render(<Row title="设备 A" onActivate={onActivate} selected />);
    const row = screen.getByRole("button", { name: "设备 A" });
    expect(row.className).toBe("row is-link is-selected");
    expect(row.getAttribute("aria-current")).toBe("true");
    expect(row.tabIndex).toBe(0);
    await user.click(row);
    expect(onActivate).toHaveBeenCalledTimes(1);
    row.focus();
    await user.keyboard("{Enter}");
    await user.keyboard(" ");
    expect(onActivate).toHaveBeenCalledTimes(3);
  });

  it("没有 onActivate 时不是按钮", () => {
    const { container } = render(<Row title="静态" />);
    expect(container.firstElementChild?.getAttribute("role")).toBeNull();
    expect(container.firstElementChild?.getAttribute("tabindex")).toBeNull();
  });

  it("FormRow 用 label 关联控件，说明文字可通过 descriptionId 引用", () => {
    render(
      <FormRow label="名称" htmlFor="name" description="用于通知标题" descriptionId="name-desc">
        <Input id="name" aria-describedby="name-desc" />
      </FormRow>,
    );
    const input = screen.getByLabelText("名称");
    expect(input.id).toBe("name");
    expect(document.getElementById("name-desc")?.textContent).toBe("用于通知标题");
    expect(input.closest(".control")).not.toBeNull();
  });

  it("FormRow 没有 htmlFor 时标题为 div", () => {
    const { container } = render(<FormRow label="开关">x</FormRow>);
    expect(container.querySelector("label")).toBeNull();
    expect(container.querySelector("div.row.form-row .row-title")?.tagName).toBe("DIV");
  });
});

describe("Table / Spinner / Empty", () => {
  it("Table 渲染 sr-only caption 与 sticky 类", () => {
    render(
      <Table sticky caption="事件列表" className="wide">
        <tbody>
          <tr>
            <td>a</td>
          </tr>
        </tbody>
      </Table>,
    );
    const table = screen.getByRole("table", { name: "事件列表" });
    expect(table.className).toBe("table is-sticky wide");
    expect(table.querySelector("caption")?.className).toBe("sr-only");
  });

  it("Spinner 以 status 暴露默认或自定义文案", () => {
    const { rerender } = render(<Spinner />);
    expect(screen.getByRole("status").textContent).toBe("加载中");
    rerender(<Spinner label="正在保存" />);
    expect(screen.getByRole("status").textContent).toBe("正在保存");
    expect(document.querySelector("svg.spinner")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("Empty 渲染图标徽记、标题、说明与操作", () => {
    const { container } = render(
      <Empty icon="bell" tone="info" title="暂无事件" action={<button type="button">刷新</button>}>
        一切正常
      </Empty>,
    );
    expect(container.querySelector(".sev.sev-info")).not.toBeNull();
    expect(container.querySelector(".t-headline")?.textContent).toBe("暂无事件");
    expect(container.querySelector(".t-subhead.muted")?.textContent).toBe("一切正常");
    expect(screen.getByRole("button", { name: "刷新" })).toBeTruthy();
  });

  it("Empty 默认 ok 色调", () => {
    const { container } = render(<Empty icon="check-circle" title="全部正常" />);
    expect(container.querySelector(".sev.sev-ok")).not.toBeNull();
  });
});
