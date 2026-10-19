"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useState, type ReactNode } from "react";
import {
  Activity,
  Building2,
  CreditCard,
  HeartPulse,
  LayoutDashboard,
  LifeBuoy,
  LogOut,
  Menu,
  Receipt,
  Tags,
  X,
} from "lucide-react";
import { useAccess } from "@karmarat/ui/react";

import { Button } from "@/components/ui/button";
import { useSession, type ConsoleIdentity } from "@/lib/session-provider";
import { cn } from "@/lib/utils";

interface NavItem {
  href: string;
  label: string;
  icon: typeof LayoutDashboard;
  /** Any one of these is enough to make the section worth showing. */
  permissions: string[];
}

const NAV: NavItem[] = [
  { href: "/", label: "Overview", icon: LayoutDashboard, permissions: ["platform_companies.read"] },
  { href: "/companies", label: "Companies", icon: Building2, permissions: ["platform_companies.read"] },
  { href: "/plans", label: "Plans", icon: Tags, permissions: ["plans.read"] },
  { href: "/subscriptions", label: "Subscriptions", icon: CreditCard, permissions: ["subscriptions.read"] },
  { href: "/payments", label: "Payments", icon: Receipt, permissions: ["payments.read"] },
  { href: "/support", label: "Support", icon: LifeBuoy, permissions: ["platform_support.read"] },
  { href: "/activity", label: "Activity", icon: Activity, permissions: ["platform_audit.read"] },
  { href: "/health", label: "Health", icon: HeartPulse, permissions: ["platform_settings.read"] },
];

export function ConsoleShell({
  identity,
  children,
}: {
  identity: ConsoleIdentity;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);

  return (
    <div className="flex min-h-dvh flex-col bg-background lg:flex-row">
      {/* Mobile bar. The sidebar is a panel below 1024px rather than a drawer
          that hides the page it belongs to. */}
      <div className="flex items-center justify-between border-b border-shell-border bg-shell px-4 py-3 text-shell-foreground lg:hidden">
        <Link href="/" className="text-sm font-semibold tracking-tight">
          Karmarat <span className="text-shell-muted">Console</span>
        </Link>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={() => setOpen((value) => !value)}
          aria-expanded={open}
          aria-controls="console-nav"
          className="text-shell-foreground hover:bg-shell-border"
        >
          {open ? <X aria-hidden /> : <Menu aria-hidden />}
          <span className="sr-only">{open ? "Close navigation" : "Open navigation"}</span>
        </Button>
      </div>

      <Sidebar id="console-nav" open={open} onNavigate={() => setOpen(false)} identity={identity} />

      <main className="min-w-0 flex-1">
        <div className="page mx-auto w-full max-w-7xl px-4 py-6 sm:px-6 lg:px-8">{children}</div>
      </main>
    </div>
  );
}

function Sidebar({
  id,
  open,
  onNavigate,
  identity,
}: {
  id: string;
  open: boolean;
  onNavigate: () => void;
  identity: ConsoleIdentity;
}) {
  const pathname = usePathname();
  const access = useAccess();
  const { signOut } = useSession();

  // Sections the operator holds no permission for are hidden, so the console is
  // not a maze of dead ends. The API is what actually refuses them.
  const visible = NAV.filter((item) => access.canAny(item.permissions));

  return (
    <nav
      id={id}
      aria-label="Console sections"
      className={cn(
        "flex-col gap-1 border-r border-shell-border bg-shell px-3 py-4 text-shell-foreground lg:flex lg:w-60 lg:shrink-0",
        open ? "flex" : "hidden",
      )}
    >
      <Link
        href="/"
        onClick={onNavigate}
        className="mb-4 hidden px-2 text-sm font-semibold tracking-tight lg:block"
      >
        Karmarat <span className="text-shell-muted">Console</span>
      </Link>

      <ul className="flex flex-col gap-0.5">
        {visible.map((item) => {
          // Exact match for the overview; prefix match elsewhere so a detail
          // page keeps its section highlighted.
          const active = item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
          const Icon = item.icon;

          return (
            <li key={item.href}>
              <Link
                href={item.href}
                onClick={onNavigate}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex items-center gap-2.5 rounded-xs px-2.5 py-2 text-sm transition-colors",
                  "focus-visible:ring-2 focus-visible:ring-shell-active focus-visible:ring-offset-0",
                  active
                    ? "bg-shell-active font-semibold text-shell-active-foreground"
                    : "text-shell-muted hover:bg-shell-border hover:text-shell-foreground",
                )}
              >
                <Icon className="size-4 shrink-0" aria-hidden />
                {item.label}
              </Link>
            </li>
          );
        })}
      </ul>

      <div className="mt-auto space-y-3 border-t border-shell-border pt-4">
        <div className="px-2">
          <p className="truncate text-sm font-medium">{identity.fullName}</p>
          <p className="truncate text-xs text-shell-muted">{identity.email}</p>
          <p className="mt-1 truncate text-[11px] uppercase tracking-wide text-shell-muted">
            {identity.roleNames.length > 0 ? identity.roleNames.join(", ") : identity.roles.join(", ")}
          </p>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => void signOut()}
          className="w-full justify-start text-shell-muted hover:bg-shell-border hover:text-shell-foreground"
        >
          <LogOut aria-hidden />
          Sign out
        </Button>
      </div>
    </nav>
  );
}
