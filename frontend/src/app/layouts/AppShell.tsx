import {
  Suspense,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  Link,
  NavLink,
  Outlet,
  useLocation,
} from "react-router-dom";
import {
  ChevronLeft,
  Home,
  Image,
  Inbox,
  Layers,
  LayoutDashboard,
  LogOut,
  Menu,
  Settings,
  Settings2,
  Blocks,
  GitBranch,
  History,
  Palette,
  ShieldCheck,
} from "lucide-react";
import { useAuth } from "@/app/auth";
import { ThemeToggle } from "@/components/common/theme-toggle";
import { BrandSymbol } from "@/components/common/brand-symbol";
import { LoadingPage } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

const SIDEBAR_KEY = "we_admin_sidebar_collapsed";

interface NavItem {
  to: string;
  label: string;
  icon: typeof Home;
  end?: boolean;
  match?: (pathname: string) => boolean;
}

const homeNav: NavItem[] = [
  { to: "/", label: "Главная", icon: LayoutDashboard, end: true },
  { to: "/media", label: "Файлы", icon: Image, match: (p) => p.startsWith("/media") },
];

const siteSettingsNav: NavItem[] = [
  { to: "/site/pages", label: "Страницы и маршруты", icon: GitBranch, match: (p) => p === "/site/pages" || p === "/site/router" },
  { to: "/site/features", label: "Реестр фичей", icon: Blocks, match: (p) => p === "/site/features" },
  { to: "/site/snapshots", label: "Снапшоты", icon: History, match: (p) => p === "/site/snapshots" },
  { to: "/site/design-system", label: "Дизайн-система", icon: Palette, match: (p) => p === "/site/design-system" },
];

const siteMetadataNav: NavItem[] = [
  { to: "/site-metadata", label: "Настройки сайта", icon: Settings2, match: (p) => p.startsWith("/site-metadata") },
];

const administrationNav: NavItem[] = [
  { to: "/access/tokens", label: "Доступы", icon: ShieldCheck, match: (p) => p.startsWith("/access") },
  { to: "/settings", label: "Настройки", icon: Settings, match: (p) => p.startsWith("/settings") },
];

const contentNav: NavItem[] = [
  { to: "/content", label: "Редактор", icon: Layers, match: (p) => p === "/content" },
  { to: "/submissions", label: "Заявки", icon: Inbox, match: (p) => p.startsWith("/submissions") },
];

function Brand({ compact = false }: { compact?: boolean }) {
  return (
    <Link
      to="/"
      className="flex min-w-0 items-center gap-2 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      aria-label="Слой — на главную"
    >
      <BrandSymbol className="size-8 text-sidebar-foreground" />
      {!compact && (
        <span className="min-w-0 leading-tight">
          <span className="block truncate text-sm font-semibold">Слой</span>
        </span>
      )}
    </Link>
  );
}

function NavList({
  collapsed,
  onNavigate,
  items,
}: {
  collapsed: boolean;
  onNavigate?: () => void;
  items: NavItem[];
}) {
  const location = useLocation();
  return (
    <ul className="space-y-1">
      {items.map((item) => {
        const Icon = item.icon;
        const active = item.match
          ? item.match(location.pathname)
          : item.end
            ? location.pathname === item.to
            : location.pathname.startsWith(item.to);
        const link = (
          <NavLink
            to={item.to}
            end={item.end}
            onClick={onNavigate}
            data-active={active}
            className={cn(
              "sidebar-link",
              collapsed && "mx-auto h-9 w-9 justify-center gap-0 rounded-lg px-0",
            )}
            aria-current={active ? "page" : undefined}
          >
            <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
            <span
              className={cn(
                "min-w-0 overflow-hidden whitespace-nowrap transition-[max-width,opacity] duration-200 ease-in-out motion-reduce:transition-none",
                collapsed ? "max-w-0 opacity-0" : "max-w-[12.5rem] opacity-100",
              )}
            >
              {item.label}
            </span>
          </NavLink>
        );

        return (
          <li key={item.to}>
            {collapsed ? (
              <Tooltip>
                <TooltipTrigger asChild>{link}</TooltipTrigger>
                <TooltipContent side="right">{item.label}</TooltipContent>
              </Tooltip>
            ) : (
              link
            )}
          </li>
        );
      })}
    </ul>
  );
}

function NavSectionLabel({
  collapsed,
  children,
}: {
  collapsed: boolean;
  children: string;
}) {
  return (
    <div className="relative flex h-4 items-center px-3">
      <p
        aria-hidden={collapsed}
        className={cn(
          "truncate text-xs font-semibold text-sidebar-muted transition-opacity duration-150 motion-reduce:transition-none",
          collapsed ? "opacity-0" : "opacity-100",
        )}
      >
        {children}
      </p>
      <span
        aria-hidden="true"
        className={cn(
          "absolute left-2 right-2 top-1/2 border-t border-sidebar-border transition-opacity duration-150 motion-reduce:transition-none",
          collapsed ? "opacity-100" : "opacity-0",
        )}
      />
    </div>
  );
}

function SidebarContent({
  collapsed,
  onNavigate,
}: {
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  const { principal, logout } = useAuth();
  const siteManager = principal?.role === "admin" || principal?.role === "moderator";
  const logoutButton = (
    <Button
      variant="ghost"
      size={collapsed ? "icon" : "sm"}
      onClick={() => logout()}
      className={cn(
        "rounded-xl text-sidebar-muted hover:text-sidebar-foreground",
        collapsed ? "mx-auto size-10 px-0" : "min-w-0 flex-1 justify-start",
      )}
      aria-label="Выйти"
    >
      <LogOut className="size-4" />
      {!collapsed && <span>Выйти</span>}
    </Button>
  );
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div
        className="flex h-14 shrink-0 items-center justify-start border-b border-sidebar-border px-4"
      >
        <Brand compact={collapsed} />
      </div>

      <nav
        className="flex-1 overflow-y-auto px-3.5 pb-3 pt-4 thin-scrollbar"
        aria-label="Основная навигация"
      >
        <div>
          <NavSectionLabel collapsed={collapsed}>Обзор</NavSectionLabel>
          <NavList collapsed={collapsed} items={homeNav} onNavigate={onNavigate} />
        </div>
        <div className="mt-3">
          <NavSectionLabel collapsed={collapsed}>Контент</NavSectionLabel>
          <NavList collapsed={collapsed} items={contentNav} onNavigate={onNavigate} />
        </div>
        <div className="mt-3">
          <NavSectionLabel collapsed={collapsed}>Сайт</NavSectionLabel>
          <NavList collapsed={collapsed} items={siteMetadataNav} onNavigate={onNavigate} />
          {siteManager && <NavList collapsed={collapsed} items={siteSettingsNav} onNavigate={onNavigate} />}
        </div>
        {siteManager && (
          <div className="mt-3">
            <NavSectionLabel collapsed={collapsed}>Администрирование</NavSectionLabel>
            <NavList collapsed={collapsed} items={administrationNav} onNavigate={onNavigate} />
          </div>
        )}
      </nav>

      <div className="shrink-0 border-t border-sidebar-border p-3">
        {principal && (
          <div className={cn("flex gap-1", collapsed ? "flex-col items-center" : "items-center justify-between")}>
            {collapsed && <ThemeToggle collapsed className="text-sidebar-muted hover:text-sidebar-foreground" />}
            {collapsed ? (
              <Tooltip>
                <TooltipTrigger asChild>{logoutButton}</TooltipTrigger>
                <TooltipContent side="right">Выйти</TooltipContent>
              </Tooltip>
            ) : logoutButton}
            {!collapsed && <ThemeToggle className="text-sidebar-muted hover:text-sidebar-foreground" />}
          </div>
        )}
      </div>
    </div>
  );
}

export function AppShell() {
  const location = useLocation();
  const routeCanvas = location.pathname === "/site/pages" || location.pathname === "/site/router";
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return window.localStorage.getItem(SIDEBAR_KEY) === "true";
    } catch {
      return false;
    }
  });
  const [mobileOpen, setMobileOpen] = useState(false);
  const mobileMenuButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    setMobileOpen(false);
  }, [location.pathname]);

  useEffect(() => {
    try {
      window.localStorage.setItem(SIDEBAR_KEY, String(collapsed));
    } catch {
      // ignore persistence failures
    }
  }, [collapsed]);

  return (
    <div className={`flex bg-background ${routeCanvas ? "h-dvh min-h-0 overflow-hidden" : "min-h-dvh"}`}>
      <a
        href="#main-content"
        onClick={(event) => {
          event.preventDefault();
          document.getElementById("main-content")?.focus();
        }}
        className="sr-only z-50 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground focus:not-sr-only focus:fixed focus:left-3 focus:top-3"
      >
        Перейти к содержимому
      </a>

      <aside
        className={cn(
          "relative sticky top-0 hidden h-dvh shrink-0 border-r border-sidebar-border bg-sidebar text-sidebar-foreground transition-[width] duration-200 ease-in-out motion-reduce:transition-none lg:flex lg:flex-col",
          collapsed ? "w-[var(--sidebar-collapsed)]" : "w-[var(--sidebar-width)]",
        )}
      >
        <SidebarContent collapsed={collapsed} />
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              onClick={() => setCollapsed((value) => !value)}
              aria-label={collapsed ? "Развернуть навигацию" : "Свернуть навигацию"}
              aria-expanded={!collapsed}
              className="absolute left-full top-1/2 z-50 inline-flex size-7 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full border border-sidebar-border bg-sidebar text-sidebar-muted shadow-sm transition-[background-color,color,box-shadow] hover:bg-sidebar-accent hover:text-sidebar-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <ChevronLeft className={cn("size-3.5 transition-transform duration-200 ease-in-out motion-reduce:transition-none", collapsed && "rotate-180")} />
            </button>
          </TooltipTrigger>
          <TooltipContent side="right">
            {collapsed ? "Развернуть навигацию" : "Свернуть навигацию"}
          </TooltipContent>
        </Tooltip>
      </aside>

      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <SheetContent
          side="left"
          hideClose
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            mobileMenuButton.current?.focus();
          }}
          className="w-[85%] max-w-[19rem] border-sidebar-border bg-sidebar p-0 text-sidebar-foreground"
        >
          <SheetHeader className="sr-only">
            <SheetTitle>Меню</SheetTitle>
            <SheetDescription>Переход к разделам панели управления.</SheetDescription>
          </SheetHeader>
          <SidebarContent
            collapsed={false}
            onNavigate={() => setMobileOpen(false)}
          />
        </SheetContent>
      </Sheet>

      <div className="fixed left-3 top-3 z-40 lg:hidden">
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              ref={mobileMenuButton}
              variant="outline"
              size="icon"
              className="size-10 rounded-xl border-border/80 bg-background/90 shadow-sm backdrop-blur-sm"
              onClick={() => setMobileOpen(true)}
              aria-label="Открыть меню"
            >
              <Menu />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="right">Открыть меню</TooltipContent>
        </Tooltip>
      </div>

      <div className="flex min-w-0 flex-1 flex-col">
        <main
          id="main-content"
          tabIndex={-1}
          className={`min-w-0 flex-1 pt-14 focus-visible:outline-none lg:pt-0 ${routeCanvas ? "min-h-0 overflow-hidden" : ""}`}
        >
          <Suspense fallback={<LoadingPage />}>
            <Outlet />
          </Suspense>
        </main>
      </div>
    </div>
  );
}
