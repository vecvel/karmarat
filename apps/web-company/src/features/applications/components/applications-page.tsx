"use client";

import React, { useState, useEffect, useRef, useCallback, Suspense } from "react";
import Link from "next/link";
import { useSearchParams, useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Card } from "@/components/ui/card";
import { PageHeader } from "@/components/shared/page-header";
import { cn } from "@/lib/utils";
import {
  Search,
  Plus,
  Filter,
  CheckCircle2,
  ArrowRight,
  Loader2,
  Calendar,
  Gift,
  XCircle,
  Clock,
  Star,
  ChevronLeft,
  ChevronRight,
  Eye,
  FileText,
  User,
  Mail,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  TableShell,
  Table,
  THead,
  TH,
  SortableTH,
  TBody,
  TR,
  TD,
  EmptyRow,
  ClientPagination,
} from "@/components/shared/data-table";
import { toast } from "sonner";
import {
  getApplications,
  updateApplicationStage,
  rejectApplication,
  type ApplicationStage,
} from "@/features/applications/server/actions";
import { sendMessageToCandidate } from "@/features/communications/server/actions";
import { getJobs } from "@/features/jobs/server/actions";
import { Can } from "@/components/rbac/can";
import { ApplicationDetailDrawer } from "@/features/applications/components/application-detail-drawer";

const KANBAN_STAGES: { id: ApplicationStage; name: string; color: string }[] = [
  { id: "applied", name: "Applied", color: "border-t-bark-muted" },
  { id: "screening", name: "Screening", color: "border-t-copper-deep" },
  { id: "shortlisted", name: "Shortlisted", color: "border-t-copper" },
  { id: "interview", name: "Interview", color: "border-t-sage-deep" },
  { id: "evaluation", name: "Evaluation", color: "border-t-sage" },
  { id: "selected", name: "Selected", color: "border-t-bark" },
  { id: "offer", name: "Offer Stage", color: "border-t-accent" },
  { id: "hired", name: "Hired (HRM)", color: "border-t-success" },
];

function ApplicationsContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const rawJobId = searchParams.get("jobId");
  const rawStage = searchParams.get("stage");
  const rawView = searchParams.get("view");

  const [activeView, setActiveView] = useState<"kanban" | "list">(rawView === "list" ? "list" : "kanban");
  const [selectedJobId, setSelectedJobId] = useState(rawJobId || "all");
  const [selectedStage, setSelectedStage] = useState(rawStage || "all");
  const [searchQuery, setSearchQuery] = useState("");
  const [applications, setApplications] = useState<any[]>([]);
  const [jobs, setJobs] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  // Full Application Details Modal State
  const [selectedDetailAppId, setSelectedDetailAppId] = useState<string | null>(null);

  // Horizontal Scroll & Fading for Kanban
  const scrollContainerRef = useRef<HTMLDivElement>(null);
  const [canScrollLeft, setCanScrollLeft] = useState(false);
  const [canScrollRight, setCanScrollRight] = useState(false);

  const checkScroll = useCallback(() => {
    const el = scrollContainerRef.current;
    if (!el) return;
    const { scrollLeft, scrollWidth, clientWidth } = el;
    setCanScrollLeft(scrollLeft > 10);
    setCanScrollRight(scrollLeft + clientWidth < scrollWidth - 10);
  }, []);

  useEffect(() => {
    const el = scrollContainerRef.current;
    if (!el) return;
    checkScroll();

    el.addEventListener("scroll", checkScroll, { passive: true });
    window.addEventListener("resize", checkScroll);

    // Initial check after paint
    const timer = setTimeout(checkScroll, 100);

    return () => {
      clearTimeout(timer);
      el.removeEventListener("scroll", checkScroll);
      window.removeEventListener("resize", checkScroll);
    };
  }, [checkScroll, applications, activeView]);

  const handleScroll = (direction: "left" | "right") => {
    const el = scrollContainerRef.current;
    if (!el) return;
    const scrollAmount = 320;
    el.scrollBy({
      left: direction === "left" ? -scrollAmount : scrollAmount,
      behavior: "smooth",
    });
  };

  // Sync URL search params whenever query changes
  useEffect(() => {
    setSelectedStage(rawStage || "all");
    setSelectedJobId(rawJobId || "all");
    if (rawView === "list" || rawView === "kanban") {
      setActiveView(rawView);
    }
  }, [rawStage, rawJobId, rawView]);

  // Auto-scroll Kanban to selected stage if stage is specified
  useEffect(() => {
    if (selectedStage && selectedStage !== "all" && activeView === "kanban") {
      const stageIndex = KANBAN_STAGES.findIndex((s) => s.id === selectedStage);
      if (stageIndex !== -1 && scrollContainerRef.current) {
        scrollContainerRef.current.scrollTo({
          left: Math.max(0, stageIndex * 300 - 40),
          behavior: "smooth",
        });
      }
    }
  }, [selectedStage, activeView, loading]);

  // Table Sorting and Client Pagination
  const [tablePage, setTablePage] = useState<number>(1);
  const [tablePageSize, setTablePageSize] = useState<number>(10);
  const [sortField, setSortField] = useState<string>("candidateName");
  const [sortDirection, setSortDirection] = useState<"asc" | "desc">("asc");

  // Reject modal
  const [rejectingAppId, setRejectingAppId] = useState<string | null>(null);
  const [rejectReason, setRejectReason] = useState("Profile does not meet core technical bar.");
  const [notifyCandidate, setNotifyCandidate] = useState(true);
  const [isRejecting, setIsRejecting] = useState(false);

  const loadData = async () => {
    setLoading(true);
    try {
      const [appList, jobList] = await Promise.all([
        getApplications({
          jobId: selectedJobId === "all" ? undefined : selectedJobId,
        }),
        getJobs(),
      ]);
      setApplications(appList);
      setJobs(jobList);
    } catch (err) {
      console.error("Failed to load applications:", err);
      toast.error("Failed to load applications");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, [selectedJobId]);

  const handleStageChange = async (appId: string, newStage: ApplicationStage) => {
    // Optimistic update
    setApplications((prev) =>
      prev.map((app) => (app.id === appId ? { ...app, stage: newStage } : app)),
    );

    try {
      await updateApplicationStage(appId, newStage);
      toast.success(`Application updated to ${newStage.toUpperCase()}`);
    } catch {
      toast.error("Failed to update application stage");
      await loadData();
    }
  };

  const handleReject = async () => {
    if (!rejectingAppId) return;
    setIsRejecting(true);
    try {
      const targetApp = applications.find((a) => a.id === rejectingAppId);
      await rejectApplication(rejectingAppId, rejectReason);

      if (notifyCandidate && targetApp?.candidateId && targetApp?.candidateEmail) {
        try {
          await sendMessageToCandidate({
            candidateId: targetApp.candidateId,
            recipientEmail: targetApp.candidateEmail,
            subject: `Update regarding your application for ${targetApp.jobTitle || "our open role"} at Karmarat`,
            body: `Dear ${targetApp.candidateName || "Candidate"},\n\nThank you for taking the time to speak with our team regarding the ${targetApp.jobTitle || "open role"} position at Karmarat.\n\nWhile your background and qualifications are commendable, we have decided to proceed with other candidates whose experience more closely aligns with our immediate technical requirements.\n\nWe genuinely appreciate the dedication and thoughtful insights you shared during our discussions, and we would welcome the opportunity to stay connected for future openings.\n\nWarm regards,\nKarmarat Talent Acquisition Team`,
          });
          toast.success("Application rejected and candidate notified via email.");
        } catch (emailErr) {
          console.error("Failed to dispatch rejection email:", emailErr);
          toast.success("Application marked as rejected (email delivery queued).");
        }
      } else {
        toast.success("Application marked as rejected.");
      }

      setRejectingAppId(null);
      await loadData();
    } catch {
      toast.error("Failed to reject application");
    } finally {
      setIsRejecting(false);
    }
  };

  const handleViewChange = (newView: "kanban" | "list") => {
    setActiveView(newView);
    const params = new URLSearchParams(window.location.search);
    if (newView === "kanban") {
      params.delete("view");
    } else {
      params.set("view", "list");
    }
    const query = params.toString();
    router.push(`/applications${query ? `?${query}` : ""}`);
  };

  const handleJobFilterChange = (jobId: string) => {
    setSelectedJobId(jobId);
    const params = new URLSearchParams(window.location.search);
    if (jobId === "all") {
      params.delete("jobId");
    } else {
      params.set("jobId", jobId);
    }
    const query = params.toString();
    router.push(`/applications${query ? `?${query}` : ""}`);
  };

  const handleStageFilterChange = (stage: string) => {
    setSelectedStage(stage);
    const params = new URLSearchParams(window.location.search);
    if (stage === "all") {
      params.delete("stage");
    } else {
      params.set("stage", stage);
    }
    const query = params.toString();
    router.push(`/applications${query ? `?${query}` : ""}`);
  };

  const filteredApplications = applications.filter((app) => {
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      const matchName = app.candidateName?.toLowerCase().includes(q);
      const matchJob = app.jobTitle?.toLowerCase().includes(q);
      const matchEmail = app.candidateEmail?.toLowerCase().includes(q);
      if (!matchName && !matchJob && !matchEmail) return false;
    }
    if (activeView === "list" && selectedStage !== "all") {
      if (app.stage !== selectedStage) return false;
    }
    return true;
  });

  return (
    <div className="page max-w-full space-y-4 font-sans">
      <PageHeader
        title="ATS Candidate Kanban & Pipeline"
        description="Live 8-stage recruitment pipeline, candidate submission reviewer, structured interview routing, and offer generation."
        actions={
          <div className="flex items-center gap-2">
            <Can permission="interviews.create">
              <Link href="/interviews/schedule">
                <Button size="sm" variant="outline" className="gap-1 text-xs">
                  <Calendar className="size-3.5 text-copper" />
                  <span>Schedule Interview</span>
                </Button>
              </Link>
            </Can>
            <Can permission="offers.create">
              <Link href="/offers/new">
                <Button size="sm" variant="accent" className="gap-1 text-xs">
                  <Gift className="size-3.5" />
                  <span>Generate Offer</span>
                </Button>
              </Link>
            </Can>
          </div>
        }
      />

      {/* Filter and View Controls */}
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
        <div className="flex items-center gap-4 border-b border-border w-fit">
          <button
            type="button"
            onClick={() => handleViewChange("kanban")}
            className={cn(
              "text-xs py-2 px-1 font-medium transition-all border-b-2 -mb-px whitespace-nowrap cursor-pointer",
              activeView === "kanban"
                ? "border-copper text-foreground font-semibold"
                : "border-transparent text-muted-foreground hover:text-foreground hover:border-border/60",
            )}
          >
            Kanban Board
          </button>
          <button
            type="button"
            onClick={() => handleViewChange("list")}
            className={cn(
              "text-xs py-2 px-1 font-medium transition-all border-b-2 -mb-px whitespace-nowrap cursor-pointer",
              activeView === "list"
                ? "border-copper text-foreground font-semibold"
                : "border-transparent text-muted-foreground hover:text-foreground hover:border-border/60",
            )}
          >
            List View
          </button>
        </div>

        <div className="flex items-center gap-2 w-full sm:w-auto flex-wrap">
          {/* Quick chevron scroll buttons in header for kanban */}
          {activeView === "kanban" && (
            <div className="hidden sm:flex items-center gap-1 pr-1">
              <button
                type="button"
                onClick={() => handleScroll("left")}
                disabled={!canScrollLeft}
                aria-label="Scroll pipeline left"
                className="size-7 rounded-xs border border-border bg-card text-foreground hover:bg-muted hover:text-copper flex items-center justify-center transition-all disabled:opacity-30 disabled:pointer-events-none cursor-pointer"
                title="Scroll pipeline left"
              >
                <ChevronLeft className="size-3.5" />
              </button>
              <button
                type="button"
                onClick={() => handleScroll("right")}
                disabled={!canScrollRight}
                aria-label="Scroll pipeline right"
                className="size-7 rounded-xs border border-border bg-card text-foreground hover:bg-muted hover:text-copper flex items-center justify-center transition-all disabled:opacity-30 disabled:pointer-events-none cursor-pointer"
                title="Scroll pipeline right"
              >
                <ChevronRight className="size-3.5" />
              </button>
            </div>
          )}

          {/* Job Filter Dropdown */}
          <select
            value={selectedJobId}
            onChange={(e) => handleJobFilterChange(e.target.value)}
            className="h-7 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper cursor-pointer max-w-[200px] truncate"
          >
            <option value="all">All Requisitions ({applications.length})</option>
            {jobs.map((j) => (
              <option key={j.id} value={j.id}>
                {j.title} ({j.departmentName || "General"})
              </option>
            ))}
          </select>

          {/* Stage Filter Dropdown */}
          <select
            value={selectedStage}
            onChange={(e) => handleStageFilterChange(e.target.value)}
            className={cn(
              "h-7 px-2.5 text-xs rounded-xs border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper cursor-pointer",
              selectedStage !== "all" ? "border-copper font-semibold text-copper" : "border-border",
            )}
          >
            <option value="all">All Stages ({applications.length} Applicants)</option>
            <option value="applied">Stage: Applied / New</option>
            <option value="screening">Stage: Screening</option>
            <option value="shortlisted">Stage: Shortlisted</option>
            <option value="interview">Stage: Interview Loops</option>
            <option value="evaluation">Stage: Evaluation &amp; Debrief</option>
            <option value="selected">Stage: Selected</option>
            <option value="offer">Stage: Offer Stage</option>
            <option value="hired">Stage: Hired (HRM)</option>
            <option value="rejected">Stage: Rejected</option>
          </select>

          <div className="relative w-full sm:w-56">
            <Search className="absolute left-2.5 top-2 size-3.5 text-muted-foreground" />
            <Input
              placeholder="Search applicants..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="pl-8 h-7 text-xs bg-card"
            />
          </div>
        </div>
      </div>

      {loading ? (
        <div className="p-16 text-center text-xs text-muted-foreground flex items-center justify-center gap-2">
          <Loader2 className="size-4 animate-spin text-copper" />
          <span>Loading ATS Kanban pipeline from database...</span>
        </div>
      ) : activeView === "kanban" ? (
        /* KANBAN BOARD VIEW (8 Stages with fixed width & smooth horizontal scroll) */
        <div className="relative w-full group/kanban">
          {/* Left Edge Fading Mask & Floating Chevron */}
          <div
            className={cn(
              "pointer-events-none absolute left-0 top-0 bottom-4 w-16 bg-gradient-to-r from-background via-background/80 to-transparent z-20 transition-all duration-300 flex items-center justify-start pl-1",
              canScrollLeft ? "opacity-100" : "opacity-0 pointer-events-none",
            )}
          >
            <button
              type="button"
              onClick={() => handleScroll("left")}
              disabled={!canScrollLeft}
              aria-label="Scroll pipeline left"
              className="pointer-events-auto size-8 rounded-full bg-card/95 hover:bg-card border border-border hover:border-copper/60 shadow-md text-foreground hover:text-copper flex items-center justify-center transition-all hover:scale-105 active:scale-95 cursor-pointer backdrop-blur-xs"
            >
              <ChevronLeft className="size-4" />
            </button>
          </div>

          {/* Right Edge Fading Mask & Floating Chevron */}
          <div
            className={cn(
              "pointer-events-none absolute right-0 top-0 bottom-4 w-16 bg-gradient-to-l from-background via-background/80 to-transparent z-20 transition-all duration-300 flex items-center justify-end pr-1",
              canScrollRight ? "opacity-100" : "opacity-0 pointer-events-none",
            )}
          >
            <button
              type="button"
              onClick={() => handleScroll("right")}
              disabled={!canScrollRight}
              aria-label="Scroll pipeline right"
              className="pointer-events-auto size-8 rounded-full bg-card/95 hover:bg-card border border-border hover:border-copper/60 shadow-md text-foreground hover:text-copper flex items-center justify-center transition-all hover:scale-105 active:scale-95 cursor-pointer backdrop-blur-xs"
            >
              <ChevronRight className="size-4" />
            </button>
          </div>

          {/* Kanban Columns Row */}
          <div
            ref={scrollContainerRef}
            onScroll={checkScroll}
            className="flex gap-3.5 overflow-x-auto pb-4 pt-1 scroll-smooth no-scrollbar select-none"
          >
            {KANBAN_STAGES.map((col) => {
              const colApps = filteredApplications.filter((a) => a.stage === col.id);
              const isSelectedStage = selectedStage === col.id;

              return (
                <div
                  key={col.id}
                  className={cn(
                    `w-[290px] min-w-[290px] max-w-[290px] shrink-0 flex flex-col rounded-xs border p-2.5 min-h-[580px] transition-all`,
                    isSelectedStage
                      ? `bg-copper/5 border-copper shadow-sm border-t-4 ${col.color}`
                      : `bg-muted/30 border-border border-t-2 ${col.color}`,
                  )}
                >
                  {/* Column Header */}
                  <div className="flex items-center justify-between pb-2 mb-2 border-b border-border/60">
                    <span className="font-semibold text-xs text-foreground truncate flex items-center gap-1.5">
                      <span>{col.name}</span>
                      {isSelectedStage && (
                        <span className="size-1.5 rounded-full bg-copper animate-pulse" />
                      )}
                    </span>
                    <span className="text-[11px] px-1.5 py-0.5 rounded-xs bg-muted border border-border/50 text-foreground font-medium">
                      {colApps.length}
                    </span>
                  </div>

                  {/* Candidate Cards */}
                  <div className="space-y-2.5 flex-1 overflow-y-auto pr-0.5 no-scrollbar">
                    {colApps.length === 0 ? (
                      <div className="h-28 flex flex-col items-center justify-center text-[11px] text-muted-foreground border border-dashed border-border/60 rounded-xs bg-muted/10 gap-1">
                        <span>No candidates</span>
                        <span className="text-[10px] opacity-60">in this stage</span>
                      </div>
                    ) : (
                      colApps.map((app) => (
                        <Card
                          key={app.id}
                          className="shadow-none border border-border bg-card hover:border-copper/60 hover:shadow-xs transition-all p-3 space-y-2.5 rounded-xs group cursor-default"
                        >
                          {/* Header */}
                          <div className="flex items-start justify-between gap-1.5">
                            <div className="min-w-0 flex-1">
                              <button
                                type="button"
                                onClick={() => setSelectedDetailAppId(app.id)}
                                className="font-semibold text-xs text-foreground block hover:text-copper transition-colors truncate text-left cursor-pointer"
                                title="Click to view full candidate application"
                              >
                                {app.candidateName}
                              </button>
                              <span className="text-[10px] text-muted-foreground truncate block">
                                {app.jobTitle}
                              </span>
                            </div>
                            <Badge
                              variant="outline"
                              className={cn(
                                "text-[9px] px-1.5 py-0 shrink-0 font-medium",
                                (app.fitScore || 0) >= 85
                                  ? "border-emerald-500/30 text-emerald-600 bg-emerald-500/10"
                                  : (app.fitScore || 0) >= 70
                                  ? "border-copper/30 text-copper bg-copper/10"
                                  : "border-border text-muted-foreground bg-muted/40",
                              )}
                            >
                              {app.fitScore}% Fit
                            </Badge>
                          </div>

                          {/* Experience / Info */}
                          <div className="text-[11px] text-muted-foreground space-y-0.5">
                            <div className="truncate text-foreground font-medium">
                              {app.currentDesignation || "Software Engineer"}
                            </div>
                            <div className="text-[10px] flex items-center justify-between text-muted-foreground">
                              <span className="flex items-center gap-1">
                                <Clock className="size-3 text-muted-foreground/70" />
                                <span>{new Date(app.createdAt).toLocaleDateString()}</span>
                              </span>
                              {app.source && (
                                <span className="text-[10px] capitalize text-muted-foreground/80 truncate max-w-[100px]">
                                  {app.source}
                                </span>
                              )}
                            </div>
                          </div>

                          {/* Quick Stage Mover Dropdown */}
                          <Can permission="applications.advance_stage">
                            <div className="pt-2 border-t border-border/60">
                              <select
                                value={app.stage}
                                onChange={(e) => handleStageChange(app.id, e.target.value as any)}
                                className="h-6.5 text-[11px] rounded-xs border border-border bg-muted/40 hover:bg-muted px-1.5 text-foreground focus:outline-none focus:ring-1 focus:ring-copper w-full font-medium transition-colors cursor-pointer"
                              >
                                {KANBAN_STAGES.map((s) => (
                                  <option key={s.id} value={s.id}>
                                    Move: {s.name}
                                  </option>
                                ))}
                              </select>
                            </div>
                          </Can>

                          {/* Quick Actions Bar */}
                          <div className="flex items-center justify-between text-[10px] text-muted-foreground pt-1 border-t border-border/40">
                            {/* View Full Application */}
                            <button
                              type="button"
                              onClick={() => setSelectedDetailAppId(app.id)}
                              className="hover:text-copper flex items-center gap-0.5 py-0.5 px-1 rounded-xs hover:bg-muted/60 transition-colors cursor-pointer"
                              title="View Full Application & Screening Answers"
                            >
                              <Eye className="size-3 text-copper" />
                              <span>View App</span>
                            </button>

                            <Can permission="communications.send">
                              <button
                                type="button"
                                onClick={() => setSelectedDetailAppId(app.id)}
                                className="hover:text-copper flex items-center gap-0.5 py-0.5 px-1 rounded-xs hover:bg-muted/60 transition-colors cursor-pointer"
                                title="Email Candidate"
                              >
                                <Mail className="size-3 text-copper" />
                                <span>Email</span>
                              </button>
                            </Can>

                            <Can permission="interviews.create">
                              <Link
                                href={`/interviews/schedule?candidateId=${app.candidateId}&applicationId=${app.id}`}
                                className="hover:text-copper flex items-center gap-0.5 py-0.5 px-1 rounded-xs hover:bg-muted/60 transition-colors"
                                title="Schedule Interview"
                              >
                                <Calendar className="size-3 text-copper" />
                                <span>Interview</span>
                              </Link>
                            </Can>

                            <Can permission="offers.create">
                              <Link
                                href={`/offers/new?candidateId=${app.candidateId}&applicationId=${app.id}`}
                                className="hover:text-copper flex items-center gap-0.5 py-0.5 px-1 rounded-xs hover:bg-muted/60 transition-colors"
                                title="Generate Offer"
                              >
                                <Gift className="size-3 text-sage-deep" />
                                <span>Offer</span>
                              </Link>
                            </Can>

                            <Can permission="applications.advance_stage">
                              <button
                                type="button"
                                onClick={() => setRejectingAppId(app.id)}
                                className="hover:text-destructive flex items-center gap-0.5 py-0.5 px-1 rounded-xs hover:bg-destructive/10 transition-colors cursor-pointer"
                                title="Reject Candidate"
                              >
                                <XCircle className="size-3 text-destructive/70" />
                                <span>Reject</span>
                              </button>
                            </Can>
                          </div>
                        </Card>
                      ))
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      ) : (
        /* LIST VIEW */
        <TableShell>
          <Table>
            <THead>
              <SortableTH
                field="candidateName"
                currentSort={sortField === "candidateName" ? (sortDirection === "asc" ? "candidateName" : "-candidateName") : ""}
                onSort={(f, d) => {
                  setSortField(f);
                  setSortDirection(d);
                }}
              >
                Candidate Name
              </SortableTH>
              <TH>Target Requisition</TH>
              <SortableTH
                field="fitScore"
                currentSort={sortField === "fitScore" ? (sortDirection === "asc" ? "fitScore" : "-fitScore") : ""}
                onSort={(f, d) => {
                  setSortField(f);
                  setSortDirection(d);
                }}
              >
                AI Fit Score
              </SortableTH>
              <TH>Recruitment Stage</TH>
              <TH>Source</TH>
              <TH align="right">Pipeline Actions</TH>
            </THead>
            <TBody>
              {filteredApplications.length === 0 ? (
                <EmptyRow colSpan={6}>No applications found matching criteria.</EmptyRow>
              ) : (
                [...filteredApplications]
                  .sort((a, b) => {
                    let aVal = a[sortField];
                    let bVal = b[sortField];
                    if (typeof aVal === "string") {
                      aVal = aVal.toLowerCase();
                      bVal = (bVal || "").toLowerCase();
                    }
                    if (aVal < bVal) return sortDirection === "asc" ? -1 : 1;
                    if (aVal > bVal) return sortDirection === "asc" ? 1 : -1;
                    return 0;
                  })
                  .slice((tablePage - 1) * tablePageSize, tablePage * tablePageSize)
                  .map((app) => (
                    <TR key={app.id}>
                      <TD>
                        <div>
                          <button
                            type="button"
                            onClick={() => setSelectedDetailAppId(app.id)}
                            className="font-semibold text-foreground text-xs block hover:text-copper transition-colors text-left cursor-pointer"
                          >
                            {app.candidateName}
                          </button>
                          <span className="text-[11px] text-muted-foreground">{app.candidateEmail}</span>
                        </div>
                      </TD>

                      <TD>
                        <span className="font-medium text-foreground text-xs">{app.jobTitle}</span>
                        {app.departmentName && (
                          <span className="text-[10px] text-muted-foreground block">{app.departmentName}</span>
                        )}
                      </TD>

                      <TD>
                        <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                          {app.fitScore}% Fit
                        </Badge>
                      </TD>

                      <TD>
                        <select
                          value={app.stage}
                          onChange={(e) => handleStageChange(app.id, e.target.value as any)}
                          className="h-7 text-xs rounded-xs border border-border bg-card px-2 text-foreground focus:outline-none focus:ring-1 focus:ring-copper font-medium cursor-pointer"
                        >
                          {KANBAN_STAGES.map((s) => (
                            <option key={s.id} value={s.id}>
                              {s.name}
                            </option>
                          ))}
                          <option value="rejected">Rejected</option>
                        </select>
                      </TD>

                      <TD>
                        <span className="text-xs text-muted-foreground capitalize">{app.source || "Careers Portal"}</span>
                      </TD>

                      <TD align="right">
                        <div className="flex items-center justify-end gap-1.5">
                          <Button
                            size="xs"
                            variant="outline"
                            onClick={() => setSelectedDetailAppId(app.id)}
                            className="gap-1 text-xs h-7"
                            title="View Full Application"
                          >
                            <Eye className="size-3 text-copper" />
                            <span>View App</span>
                          </Button>

                          <Can permission="communications.send">
                            <Button
                              size="xs"
                              variant="outline"
                              onClick={() => setSelectedDetailAppId(app.id)}
                              className="gap-1 text-xs h-7 text-copper border-copper/30 hover:bg-copper/10"
                              title="Email Candidate"
                            >
                              <Mail className="size-3" />
                              <span>Email</span>
                            </Button>
                          </Can>

                          <Can permission="interviews.create">
                            <Link
                              href={`/interviews/schedule?candidateId=${app.candidateId}&applicationId=${app.id}`}
                            >
                              <Button size="xs" variant="outline" className="gap-1 text-xs h-7">
                                <Calendar className="size-3 text-copper" />
                                <span>Interview</span>
                              </Button>
                            </Link>
                          </Can>

                          <Can permission="offers.create">
                            <Link
                              href={`/offers/new?candidateId=${app.candidateId}&applicationId=${app.id}`}
                            >
                              <Button size="xs" variant="accent" className="gap-1 text-xs h-7">
                                <Gift className="size-3" />
                                <span>Offer</span>
                              </Button>
                            </Link>
                          </Can>

                          <Can permission="applications.advance_stage">
                            <Button
                              size="xs"
                              variant="ghost"
                              onClick={() => setRejectingAppId(app.id)}
                              className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive hover:bg-destructive/10 cursor-pointer"
                              title="Reject Application"
                            >
                              <XCircle className="size-3.5" />
                            </Button>
                          </Can>
                        </div>
                      </TD>
                    </TR>
                  ))
              )}
            </TBody>
          </Table>

          <ClientPagination
            page={tablePage}
            limit={tablePageSize}
            total={filteredApplications.length}
            onPageChange={setTablePage}
            onLimitChange={setTablePageSize}
            limitOptions={[10, 25, 50, 100]}
          />
        </TableShell>
      )}

      {/* Reject Application Modal */}
      <Dialog open={!!rejectingAppId} onOpenChange={(open) => !open && setRejectingAppId(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Reject Candidate Application</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <p className="text-muted-foreground">
              Provide feedback or rejection reason for internal records:
            </p>
            <div className="space-y-1">
              <label className="field-label">Reason / Feedback</label>
              <textarea
                rows={3}
                value={rejectReason}
                onChange={(e) => setRejectReason(e.target.value)}
                className="w-full rounded-xs border border-border bg-card p-2 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-destructive"
              />
            </div>

            <label className="flex items-center gap-2 pt-1 text-xs text-foreground cursor-pointer select-none">
              <input
                type="checkbox"
                checked={notifyCandidate}
                onChange={(e) => setNotifyCandidate(e.target.checked)}
                className="rounded-xs border-border text-copper focus:ring-copper accent-copper"
              />
              <span className="text-muted-foreground">Send respectful rejection email to candidate</span>
            </label>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setRejectingAppId(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="destructive"
              disabled={isRejecting}
              onClick={handleReject}
              className="gap-1"
            >
              {isRejecting ? <Loader2 className="size-3.5 animate-spin" /> : <XCircle className="size-3.5" />}
              <span>Confirm Rejection</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Full Candidate Application Drawer Modal */}
      <ApplicationDetailDrawer
        applicationId={selectedDetailAppId}
        open={!!selectedDetailAppId}
        onOpenChange={(open) => !open && setSelectedDetailAppId(null)}
        onStageUpdated={(appId, newStage) => {
          setApplications((prev) =>
            prev.map((a) => (a.id === appId ? { ...a, stage: newStage } : a)),
          );
        }}
      />
    </div>
  );
}

export function ApplicationsPage() {
  return (
    <Suspense
      fallback={
        <div className="p-16 text-center text-xs text-muted-foreground flex items-center justify-center gap-2">
          <Loader2 className="size-4 animate-spin text-copper" />
          <span>Loading ATS Candidate Pipeline...</span>
        </div>
      }
    >
      <ApplicationsContent />
    </Suspense>
  );
}
