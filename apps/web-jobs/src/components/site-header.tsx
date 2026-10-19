"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { LogOut, User } from "lucide-react";

import { NotificationBell } from "@/components/notifications/notification-bell";
import { useSession } from "@/components/session-provider";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { Identity } from "@/lib/session";
import { cn } from "@/lib/utils";

const NAV = [
  { href: "/", label: "Jobs" },
  { href: "/applications", label: "Applications" },
  { href: "/messages", label: "Messages" },
  { href: "/profile", label: "Profile" },
];

/**
 * `identity` comes from the server, so the signed-in shell is correct in the
 * very first byte of HTML rather than appearing a moment after hydration.
 */
export function SiteHeader({ identity }: { identity: Identity | null }) {
  const pathname = usePathname();
  const { signOut } = useSession();
  const signedIn = identity !== null;

  return (
    <header className="sticky top-0 z-40 border-b border-shell-border bg-shell text-shell-foreground">
      <div className="mx-auto flex h-14 w-full max-w-6xl items-center gap-6 px-4">
        <Link
          href="/"
          className="text-sm font-semibold tracking-tight text-shell-foreground"
        >
          Karmarat <span className="text-shell-muted">Jobs</span>
        </Link>

        <nav aria-label="Main" className="hidden items-center gap-1 sm:flex">
          {NAV.filter((item) => signedIn || item.href === "/").map((item) => {
            const active =
              item.href === "/"
                ? pathname === "/"
                : pathname.startsWith(item.href);
            return (
              <Link
                key={item.href}
                href={item.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "rounded-xs px-3 py-1.5 text-sm transition-colors",
                  active
                    ? "bg-shell-active text-shell-active-foreground"
                    : "text-shell-muted hover:bg-shell-border hover:text-shell-foreground",
                )}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>

        <div className="ml-auto flex items-center gap-2">
          {signedIn ? (
            <>
              <NotificationBell />
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="gap-2 text-shell-muted hover:bg-shell-border hover:text-shell-foreground"
                  >
                    <User aria-hidden="true" />
                    <span className="hidden max-w-[12rem] truncate sm:inline">
                      {identity.fullName || identity.email}
                    </span>
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-56">
                  <DropdownMenuLabel className="truncate font-normal text-muted-foreground">
                    {identity.email}
                  </DropdownMenuLabel>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem asChild>
                    <Link href="/profile">Profile</Link>
                  </DropdownMenuItem>
                  <DropdownMenuItem asChild>
                    <Link href="/profile/resumes">Résumés</Link>
                  </DropdownMenuItem>
                  <DropdownMenuItem asChild>
                    <Link href="/profile/visibility">Who can find me</Link>
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem onSelect={() => void signOut()}>
                    <LogOut aria-hidden="true" />
                    Sign out
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </>
          ) : (
            <>
              <Button
                asChild
                variant="ghost"
                size="sm"
                className="text-shell-muted hover:bg-shell-border hover:text-shell-foreground"
              >
                <Link href="/sign-in">Sign in</Link>
              </Button>
              <Button asChild variant="accent" size="sm">
                <Link href="/register">Create account</Link>
              </Button>
            </>
          )}
        </div>
      </div>

      {/* The nav collapses out of the bar on small screens rather than into a
          drawer: four destinations fit on one row, and a drawer would be a
          second thing to open before reaching any of them. */}
      {signedIn ? (
        <nav
          aria-label="Main"
          className="flex items-center gap-1 overflow-x-auto border-t border-shell-border px-4 py-1.5 sm:hidden"
        >
          {NAV.map((item) => {
            const active =
              item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
            return (
              <Link
                key={item.href}
                href={item.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "shrink-0 rounded-xs px-3 py-1.5 text-sm",
                  active
                    ? "bg-shell-active text-shell-active-foreground"
                    : "text-shell-muted",
                )}
              >
                {item.label}
              </Link>
            );
          })}
        </nav>
      ) : null}
    </header>
  );
}
