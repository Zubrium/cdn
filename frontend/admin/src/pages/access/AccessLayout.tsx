import { NavLink, Outlet, useLocation } from "react-router-dom";
import { KeyRound, ShieldCheck, UsersRound } from "lucide-react";
import { cn } from "@/lib/utils";
import { PageHeader, useDocumentTitle } from "@/components/common/page";

const tabs = [
  { to: "/access/tokens", label: "Токены", icon: KeyRound },
  { to: "/access/users", label: "Пользователи", icon: UsersRound },
  { to: "/access/roles", label: "Роли", icon: ShieldCheck },
];

export default function AccessLayout() {
  const location = useLocation();
  const active = tabs.find((tab) => location.pathname.startsWith(tab.to));
  useDocumentTitle(active ? `Доступы · ${active.label}` : "Доступы");

  return (
    <div className="page-grid">
      <PageHeader
        title="Доступы"
        description="Каждый токен — отдельная учётная запись с именем, ролью и сроком действия. Выпускайте, просматривайте и отзывайте токены на вкладке «Токены»."
      />

      <nav
        className="mb-4 inline-flex flex-wrap items-center gap-0.5 rounded-lg bg-muted p-0.5 text-muted-foreground"
        aria-label="Разделы доступов"
      >
        {tabs.map((tab) => {
          const Icon = tab.icon;
          return (
            <NavLink
              key={tab.to}
              to={tab.to}
              className={({ isActive }) =>
                cn(
                  "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  isActive
                    ? "bg-background text-foreground shadow-sm"
                    : "hover:text-foreground",
                )
              }
            >
              <Icon className="h-4 w-4" aria-hidden="true" />
              {tab.label}
            </NavLink>
          );
        })}
      </nav>

      <Outlet />
    </div>
  );
}
