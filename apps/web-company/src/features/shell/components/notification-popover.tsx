"use client";

import React, { useState, useEffect, useCallback } from "react";
import Link from "next/link";
import {
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Bell,
  CheckCircle2,
  Clock,
  User,
  Calendar,
  Gift,
  Mail,
  Star,
  Check,
  ExternalLink,
  Loader2,
  Sparkles,
  ChevronRight,
  Filter,
} from "lucide-react";
import {
  getSystemNotifications,
  markNotificationAsRead,
  markAllNotificationsAsRead,
  type SystemNotification,
} from "@/features/shell/server/notifications";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

interface NotificationPopoverProps {
  initialUnreadCount?: number;
}

const LOCAL_STORAGE_KEY = "karmarat_read_notifs";

export function NotificationPopover({ initialUnreadCount = 0 }: NotificationPopoverProps) {
  const [open, setOpen] = useState(false);
  const [notifications, setNotifications] = useState<SystemNotification[]>([]);
  const [loading, setLoading] = useState(false);
  const [unreadCount, setUnreadCount] = useState(initialUnreadCount);
  const [activeFilter, setActiveFilter] = useState<"all" | "unread" | "application" | "interview" | "offer">("all");

  const getLocalReadSet = (): Set<string> => {
    try {
      const raw = localStorage.getItem(LOCAL_STORAGE_KEY);
      if (!raw) return new Set();
      return new Set(JSON.parse(raw));
    } catch {
      return new Set();
    }
  };

  const saveLocalReadSet = (set: Set<string>) => {
    try {
      localStorage.setItem(LOCAL_STORAGE_KEY, JSON.stringify(Array.from(set).slice(-300)));
    } catch (e) {
      console.error(e);
    }
  };

  const loadNotifications = useCallback(async () => {
    setLoading(true);
    try {
      const res = await getSystemNotifications();
      const localReadSet = getLocalReadSet();

      // Merge server read status with client localStorage cache
      const merged = res.notifications.map((n) => ({
        ...n,
        read: n.read || localReadSet.has(n.id),
      }));

      setNotifications(merged);
      const computedUnread = merged.filter((n) => !n.read).length;
      setUnreadCount(computedUnread);
    } catch (err) {
      console.error("Failed to load notifications:", err);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadNotifications();
  }, [loadNotifications]);

  const handleOpenChange = (newOpen: boolean) => {
    setOpen(newOpen);
    if (newOpen) {
      loadNotifications();
    }
  };

  const handleMarkAsRead = async (id: string, e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();

    // 1. Optimistic state update
    setNotifications((prev) =>
      prev.map((n) => (n.id === id ? { ...n, read: true } : n))
    );
    setUnreadCount((prev) => Math.max(0, prev - 1));

    // 2. Cache in localStorage
    const localSet = getLocalReadSet();
    localSet.add(id);
    saveLocalReadSet(localSet);

    toast.success("Notification marked as read");

    // 3. Persist to PostgreSQL via Server Action
    await markNotificationAsRead(id);
  };

  const handleMarkAllAsRead = async () => {
    const unreadIds = notifications.filter((n) => !n.read).map((n) => n.id);

    // 1. Optimistic state update
    setNotifications((prev) => prev.map((n) => ({ ...n, read: true })));
    setUnreadCount(0);

    // 2. Cache in localStorage
    const localSet = getLocalReadSet();
    notifications.forEach((n) => localSet.add(n.id));
    saveLocalReadSet(localSet);

    toast.success("All notifications marked as read");

    // 3. Persist to PostgreSQL via Server Action
    if (unreadIds.length > 0) {
      await markAllNotificationsAsRead(unreadIds);
    }
  };

  const filteredList = notifications.filter((n) => {
    if (activeFilter === "unread") return !n.read;
    if (activeFilter === "application") return n.type === "application";
    if (activeFilter === "interview") return n.type === "interview" || n.type === "scorecard";
    if (activeFilter === "offer") return n.type === "offer";
    return true;
  });

  const getNotificationIcon = (type: SystemNotification["type"]) => {
    switch (type) {
      case "application":
        return <User className="size-3.5 text-copper" />;
      case "interview":
        return <Calendar className="size-3.5 text-sage-deep" />;
      case "scorecard":
        return <Star className="size-3.5 text-amber-500" />;
      case "offer":
        return <Gift className="size-3.5 text-accent" />;
      case "communication":
        return <Mail className="size-3.5 text-sky-500" />;
      default:
        return <Sparkles className="size-3.5 text-copper" />;
    }
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={`Notifications${unreadCount ? `, ${unreadCount} unread` : ""}`}
          className={cn(
            "relative flex size-8 shrink-0 items-center justify-center rounded-xs transition-colors cursor-pointer",
            open
              ? "bg-muted text-foreground"
              : "text-muted-foreground hover:bg-muted hover:text-foreground",
          )}
        >
          <Bell className="size-4" />
          {unreadCount > 0 && (
            <span className="absolute right-0.5 top-0.5 flex min-w-3.75 h-3.75 items-center justify-center rounded-full bg-accent px-1 text-[9px] font-bold leading-none text-accent-foreground shadow-xs animate-pulse">
              {unreadCount > 99 ? "99+" : unreadCount}
            </span>
          )}
        </button>
      </PopoverTrigger>

      <PopoverContent
        align="end"
        sideOffset={8}
        className="w-96 p-0 shadow-2xl border border-border bg-card overflow-hidden flex flex-col max-h-[540px] font-sans"
      >
        {/* Banner Header */}
        <div className="p-3.5 border-b border-border bg-muted/40 flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <Bell className="size-4 text-copper" />
            <h3 className="text-xs font-bold text-foreground tracking-tight">
              Activity &amp; Notifications
            </h3>
            {unreadCount > 0 && (
              <Badge variant="accent" className="text-[10px] px-1.5 py-0 h-4 font-semibold">
                {unreadCount} Unread
              </Badge>
            )}
          </div>

          {unreadCount > 0 && (
            <button
              type="button"
              onClick={handleMarkAllAsRead}
              className="text-[11px] text-copper hover:underline font-semibold flex items-center gap-1 cursor-pointer"
            >
              <Check className="size-3" />
              <span>Mark all read</span>
            </button>
          )}
        </div>

        {/* Category Filters */}
        <div className="flex items-center gap-1 p-2 border-b border-border/70 bg-card overflow-x-auto no-scrollbar">
          <button
            type="button"
            onClick={() => setActiveFilter("all")}
            className={cn(
              "px-2 py-0.5 text-[11px] rounded-xs font-medium transition-colors cursor-pointer whitespace-nowrap",
              activeFilter === "all"
                ? "bg-copper/15 text-copper font-semibold"
                : "text-muted-foreground hover:text-foreground hover:bg-muted/60",
            )}
          >
            All ({notifications.length})
          </button>

          <button
            type="button"
            onClick={() => setActiveFilter("unread")}
            className={cn(
              "px-2 py-0.5 text-[11px] rounded-xs font-medium transition-colors cursor-pointer whitespace-nowrap",
              activeFilter === "unread"
                ? "bg-copper/15 text-copper font-semibold"
                : "text-muted-foreground hover:text-foreground hover:bg-muted/60",
            )}
          >
            Unread ({unreadCount})
          </button>

          <button
            type="button"
            onClick={() => setActiveFilter("application")}
            className={cn(
              "px-2 py-0.5 text-[11px] rounded-xs font-medium transition-colors cursor-pointer whitespace-nowrap",
              activeFilter === "application"
                ? "bg-copper/15 text-copper font-semibold"
                : "text-muted-foreground hover:text-foreground hover:bg-muted/60",
            )}
          >
            Applications
          </button>

          <button
            type="button"
            onClick={() => setActiveFilter("interview")}
            className={cn(
              "px-2 py-0.5 text-[11px] rounded-xs font-medium transition-colors cursor-pointer whitespace-nowrap",
              activeFilter === "interview"
                ? "bg-copper/15 text-copper font-semibold"
                : "text-muted-foreground hover:text-foreground hover:bg-muted/60",
            )}
          >
            Interviews
          </button>

          <button
            type="button"
            onClick={() => setActiveFilter("offer")}
            className={cn(
              "px-2 py-0.5 text-[11px] rounded-xs font-medium transition-colors cursor-pointer whitespace-nowrap",
              activeFilter === "offer"
                ? "bg-copper/15 text-copper font-semibold"
                : "text-muted-foreground hover:text-foreground hover:bg-muted/60",
            )}
          >
            Offers
          </button>
        </div>

        {/* Notification Feed List */}
        <div className="flex-1 overflow-y-auto divide-y divide-border/50 max-h-[360px] scrollbar-thin">
          {loading ? (
            <div className="p-8 text-center text-xs text-muted-foreground flex items-center justify-center gap-2">
              <Loader2 className="size-4 animate-spin text-copper" />
              <span>Fetching latest alerts...</span>
            </div>
          ) : filteredList.length === 0 ? (
            <div className="p-8 text-center space-y-1.5 text-muted-foreground">
              <CheckCircle2 className="size-7 text-emerald-500 mx-auto opacity-70" />
              <p className="font-semibold text-xs text-foreground">You&apos;re all caught up!</p>
              <p className="text-[11px]">No active notifications in this category.</p>
            </div>
          ) : (
            filteredList.map((notif) => (
              <div
                key={notif.id}
                className={cn(
                  "p-3 transition-colors flex items-start gap-2.5 group relative hover:bg-muted/50",
                  !notif.read ? "bg-copper/5 font-medium" : "bg-card opacity-80",
                )}
              >
                {/* Icon Badge */}
                <div className="size-7 rounded-xs bg-muted border border-border flex items-center justify-center shrink-0 mt-0.5">
                  {getNotificationIcon(notif.type)}
                </div>

                {/* Content */}
                <div className="flex-1 min-w-0 space-y-1">
                  <div className="flex items-start justify-between gap-1">
                    <span className="text-xs font-semibold text-foreground truncate block leading-tight">
                      {notif.title}
                    </span>
                    <span className="text-[10px] text-muted-foreground shrink-0 whitespace-nowrap">
                      {notif.timeAgo}
                    </span>
                  </div>

                  <p className="text-[11px] text-muted-foreground line-clamp-2 leading-relaxed font-normal">
                    {notif.message}
                  </p>

                  <div className="flex items-center justify-between pt-1">
                    <Link
                      href={notif.href}
                      onClick={(e) => {
                        setOpen(false);
                        handleMarkAsRead(notif.id, e);
                      }}
                      className="inline-flex items-center gap-1 text-[10px] font-semibold text-copper hover:underline"
                    >
                      <span>{notif.actionLabel}</span>
                      <ChevronRight className="size-2.5" />
                    </Link>

                    {!notif.read && (
                      <button
                        type="button"
                        onClick={(e) => handleMarkAsRead(notif.id, e)}
                        className="text-[10px] text-muted-foreground hover:text-foreground opacity-0 group-hover:opacity-100 transition-opacity flex items-center gap-0.5 cursor-pointer"
                        title="Mark as read"
                      >
                        <Check className="size-2.5" />
                        <span>Read</span>
                      </button>
                    )}
                  </div>
                </div>

                {/* Unread indicator bar */}
                {!notif.read && (
                  <span className="absolute left-0 top-3 bottom-3 w-0.75 bg-copper rounded-r-xs" />
                )}
              </div>
            ))
          )}
        </div>

        {/* Footer */}
        <div className="p-2.5 border-t border-border bg-muted/30 flex items-center justify-between text-[11px]">
          <span className="text-muted-foreground">Karmarat Activity Feed</span>
          <Link
            href="/communications?tab=history"
            onClick={() => setOpen(false)}
            className="text-copper hover:underline font-semibold flex items-center gap-1"
          >
            <span>Delivery Audit History</span>
            <ExternalLink className="size-2.5" />
          </Link>
        </div>
      </PopoverContent>
    </Popover>
  );
}
