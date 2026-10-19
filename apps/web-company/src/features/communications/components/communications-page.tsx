"use client";

import { useState, useEffect, useMemo, Suspense } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card";
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
import { PageHeader } from "@/components/shared/page-header";
import { cn } from "@/lib/utils";
import {
  Plus,
  Mail,
  Send,
  Loader2,
  Trash2,
  Edit2,
  CheckCircle2,
  Clock,
  User,
  Eye,
  Sparkles,
  Search,
  Filter,
  Layers,
  FileText,
  Check,
  HelpCircle,
  Settings2,
  ExternalLink,
  Calendar,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { toast } from "sonner";
import {
  getCommunicationTemplates,
  createCommunicationTemplate,
  updateCommunicationTemplate,
  deleteCommunicationTemplate,
  getCandidateMessages,
  sendMessageToCandidate,
} from "@/features/communications/server/actions";
import { getCandidates } from "@/features/candidates/server/actions";
import { getSmtpConfig, type SmtpConfig } from "@/features/organization/server/actions";
import { renderEmailTemplate } from "@/lib/email/template-utils";
import { Can } from "@/components/rbac/can";
import Link from "next/link";

const MERGE_TOKENS = [
  { token: "{{candidate_name}}", label: "Full Name", desc: "e.g. Alex Rivera" },
  { token: "{{candidate_first_name}}", label: "First Name", desc: "e.g. Alex" },
  { token: "{{job_title}}", label: "Job Title", desc: "e.g. Staff Backend Engineer" },
  { token: "{{req_code}}", label: "Req Code", desc: "e.g. REQ-2026-08" },
  { token: "{{department}}", label: "Department", desc: "e.g. Engineering" },
  { token: "{{company_name}}", label: "Company", desc: "e.g. Karmarat" },
  { token: "{{work_location}}", label: "Location", desc: "e.g. San Francisco, CA" },
  { token: "{{interview_date}}", label: "Interview Date", desc: "e.g. Oct 24, 2026" },
  { token: "{{interview_time}}", label: "Interview Time", desc: "e.g. 2:00 PM EST" },
  { token: "{{meeting_link}}", label: "Meeting Link", desc: "e.g. https://meet.google.com/..." },
  { token: "{{interviewer_name}}", label: "Interviewer", desc: "e.g. Sarah Jenkins (VP)" },
  { token: "{{offer_salary}}", label: "Offer Salary", desc: "e.g. $185,000 / year" },
  { token: "{{joining_date}}", label: "Start Date", desc: "e.g. Nov 15, 2026" },
  { token: "{{sender_name}}", label: "Sender Name", desc: "e.g. Jordan Lee" },
  { token: "{{careers_url}}", label: "Careers URL", desc: "e.g. https://careers.karmarat.com" },
];

const SAMPLE_DATA: Record<string, string> = {
  candidate_name: "Alex Rivera",
  candidate_first_name: "Alex",
  job_title: "Staff Distributed Systems Engineer",
  req_code: "REQ-2026-ENG",
  department: "Infrastructure Engineering",
  company_name: "Karmarat Corp",
  work_location: "San Francisco, CA (Hybrid)",
  interview_date: "Thursday, October 24, 2026",
  interview_time: "2:00 PM - 3:00 PM PST",
  meeting_link: "https://meet.google.com/rqb-tech-panel",
  interviewer_name: "Marcus Vance (Principal Architect)",
  offer_salary: "$195,000 USD / year",
  joining_date: "November 15, 2026",
  sender_name: "Elena Rostova",
  careers_url: "https://karmarat.com/careers",
};

const STAGE_FILTERS = [
  { id: "all", label: "All Stages" },
  { id: "applied", label: "Applied / Acknowledgment" },
  { id: "screening", label: "Screening & Assessments" },
  { id: "interview", label: "Interview Loops" },
  { id: "evaluation", label: "Evaluation & Debrief" },
  { id: "selected", label: "Executive Selection" },
  { id: "offer", label: "Offer Extension" },
  { id: "hired", label: "Hired & Onboarding" },
  { id: "rejected", label: "Rejections" },
  { id: "talent_pool", label: "Talent Community" },
];

function CommunicationsContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tabParam = searchParams.get("tab");

  const normalizeTab = (t: string | null): string => {
    if (!t || t === "templates") return "templates";
    if (t === "history") return "history";
    return "templates";
  };

  const [activeTab, setActiveTab] = useState(normalizeTab(tabParam));
  const [templates, setTemplates] = useState<any[]>([]);
  const [messages, setMessages] = useState<any[]>([]);
  const [candidates, setCandidates] = useState<any[]>([]);
  const [smtpStatus, setSmtpStatus] = useState<SmtpConfig | null>(null);
  const [loading, setLoading] = useState(true);

  // Template Search & Stage Filter
  const [templateSearch, setTemplateSearch] = useState("");
  const [selectedStageFilter, setSelectedStageFilter] = useState("all");

  // History Sorting & Client Pagination
  const [historySortField, setHistorySortField] = useState<string>("sentAt");
  const [historySortDirection, setHistorySortDirection] = useState<"asc" | "desc">("desc");
  const [historyPage, setHistoryPage] = useState<number>(1);
  const [historyPageSize, setHistoryPageSize] = useState<number>(10);

  // Create/Edit Template Modal
  const [templateModalOpen, setTemplateModalOpen] = useState(false);
  const [editingTemplateId, setEditingTemplateId] = useState<string | null>(null);
  const [tplName, setTplName] = useState("");
  const [tplStage, setTplStage] = useState("applied");
  const [tplSubject, setTplSubject] = useState("");
  const [tplBody, setTplBody] = useState("");
  const [isSavingTpl, setIsSavingTpl] = useState(false);

  // Preview Simulator Modal
  const [previewModalOpen, setPreviewModalOpen] = useState(false);
  const [previewTemplate, setPreviewTemplate] = useState<any>(null);

  // Send Message Modal
  const [sendModalOpen, setSendModalOpen] = useState(false);
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [targetCandidateId, setTargetCandidateId] = useState("");
  const [msgSubject, setMsgSubject] = useState("");
  const [msgBody, setMsgBody] = useState("");
  const [isSending, setIsSending] = useState(false);

  // Message View Modal
  const [viewMessageModalOpen, setViewMessageModalOpen] = useState(false);
  const [activeViewMessage, setActiveViewMessage] = useState<any>(null);

  const loadData = async () => {
    setLoading(true);
    try {
      const [tplData, msgData, canData, smtpData] = await Promise.all([
        getCommunicationTemplates(),
        getCandidateMessages(),
        getCandidates(),
        getSmtpConfig(),
      ]);
      setTemplates(tplData);
      setMessages(msgData);
      setCandidates(canData);
      setSmtpStatus(smtpData);
      if (canData.length > 0 && !targetCandidateId) {
        setTargetCandidateId(canData[0].id);
      }
    } catch (err) {
      console.error(err);
      toast.error("Failed to load communications data");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    setActiveTab(normalizeTab(tabParam));
  }, [tabParam]);

  useEffect(() => {
    loadData();
  }, []);

  const handleTabChange = (newTab: string) => {
    setActiveTab(newTab);
    const targetUrl = newTab === "templates" ? "/communications" : `/communications?tab=${newTab}`;
    router.replace(targetUrl);
  };

  // Filtered Templates
  const filteredTemplates = useMemo(() => {
    return templates.filter((tpl) => {
      const matchesSearch =
        tpl.name.toLowerCase().includes(templateSearch.toLowerCase()) ||
        tpl.subject.toLowerCase().includes(templateSearch.toLowerCase()) ||
        tpl.triggerEvent.toLowerCase().includes(templateSearch.toLowerCase());

      const matchesStage =
        selectedStageFilter === "all" || tpl.triggerEvent === selectedStageFilter;

      return matchesSearch && matchesStage;
    });
  }, [templates, templateSearch, selectedStageFilter]);

  const handleOpenCreateTpl = () => {
    setEditingTemplateId(null);
    setTplName("");
    setTplStage("applied");
    setTplSubject("Update regarding your application for {{job_title}} at {{company_name}}");
    setTplBody("Hi {{candidate_first_name}},\n\nThank you for applying for the {{job_title}} position at {{company_name}}!\n\nBest regards,\n{{sender_name}}");
    setTemplateModalOpen(true);
  };

  const handleOpenEditTpl = (tpl: any) => {
    setEditingTemplateId(tpl.id);
    setTplName(tpl.name);
    setTplStage(tpl.triggerEvent || "applied");
    setTplSubject(tpl.subject);
    setTplBody(tpl.bodyTemplate);
    setTemplateModalOpen(true);
  };

  const handleOpenPreview = (tpl: any) => {
    setPreviewTemplate(tpl);
    setPreviewModalOpen(true);
  };

  const handleInsertToken = (token: string) => {
    setTplBody((prev) => prev + " " + token);
  };

  const handleInsertSubjectToken = (token: string) => {
    setTplSubject((prev) => prev + " " + token);
  };

  const handleSaveTemplate = async () => {
    if (!tplName || !tplSubject || !tplBody) {
      toast.error("Please fill in all template fields");
      return;
    }
    setIsSavingTpl(true);
    try {
      if (editingTemplateId) {
        await updateCommunicationTemplate(editingTemplateId, {
          name: tplName,
          triggerEvent: tplStage,
          subject: tplSubject,
          bodyTemplate: tplBody,
        });
        toast.success("Template updated successfully!");
      } else {
        await createCommunicationTemplate({
          name: tplName,
          triggerEvent: tplStage,
          subject: tplSubject,
          bodyTemplate: tplBody,
        });
        toast.success("New email template created!");
      }
      setTemplateModalOpen(false);
      await loadData();
    } catch {
      toast.error("Failed to save template");
    } finally {
      setIsSavingTpl(false);
    }
  };

  const handleDeleteTemplate = async (id: string, name: string) => {
    if (!confirm(`Delete template "${name}"?`)) return;
    try {
      await deleteCommunicationTemplate(id);
      toast.success("Template deleted");
      await loadData();
    } catch {
      toast.error("Failed to delete template");
    }
  };

  const handleTemplateSelectionForSend = (tplId: string) => {
    setSelectedTemplateId(tplId);
    if (!tplId) return;

    const tpl = templates.find((t) => t.id === tplId);
    if (!tpl) return;

    const candidate = candidates.find((c) => c.id === targetCandidateId);
    const candidateName = candidate?.fullName || "Candidate";
    const candidateFirstName = candidateName.split(" ")[0] || "Candidate";

    const vars: Record<string, string> = {
      ...SAMPLE_DATA,
      candidate_name: candidateName,
      candidate_first_name: candidateFirstName,
    };

    setMsgSubject(renderEmailTemplate(tpl.subject, vars));
    setMsgBody(renderEmailTemplate(tpl.bodyTemplate, vars));
  };

  const handleSendMessage = async () => {
    if (!targetCandidateId || !msgSubject || !msgBody) {
      toast.error("Please fill in candidate, subject, and message content");
      return;
    }
    const matchedCandidate = candidates.find((c) => c.id === targetCandidateId);
    if (!matchedCandidate) return;

    setIsSending(true);
    try {
      const res = await sendMessageToCandidate({
        candidateId: targetCandidateId,
        templateId: selectedTemplateId || undefined,
        recipientEmail: matchedCandidate.email,
        subject: msgSubject,
        body: msgBody,
      });

      if (res.sentViaSmtp) {
        toast.success("Email dispatched via live SMTP server and logged to audit trail!");
      } else {
        toast.success("Message logged to candidate audit trail! (Configure SMTP in Settings for direct mailbox dispatch)");
      }

      setSendModalOpen(false);
      await loadData();
    } catch (err: any) {
      toast.error(err.message || "Failed to send message");
    } finally {
      setIsSending(false);
    }
  };

  const handleHistorySort = (field: string, direction: "asc" | "desc") => {
    setHistorySortField(field);
    setHistorySortDirection(direction);
  };

  const sortedMessages = useMemo(() => {
    return [...messages].sort((a, b) => {
      let aVal = a[historySortField];
      let bVal = b[historySortField];

      if (typeof aVal === "string") {
        aVal = aVal.toLowerCase();
        bVal = (bVal || "").toLowerCase();
      }

      if (aVal < bVal) return historySortDirection === "asc" ? -1 : 1;
      if (aVal > bVal) return historySortDirection === "asc" ? 1 : -1;
      return 0;
    });
  }, [messages, historySortField, historySortDirection]);

  const paginatedMessages = useMemo(() => {
    const from = (historyPage - 1) * historyPageSize;
    return sortedMessages.slice(from, from + historyPageSize);
  }, [sortedMessages, historyPage, historyPageSize]);

  return (
    <div className="page space-y-4">
      <PageHeader
        title="Candidate Communications & Automated Templates"
        description="Enterprise messaging system with dynamic token interpolation, full stage triggers, and SMTP delivery routing."
        actions={
          <div className="flex items-center gap-2">
            <Can permission="communications.send">
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setSelectedTemplateId("");
                  setMsgSubject("");
                  setMsgBody("");
                  setSendModalOpen(true);
                }}
                className="gap-1 text-xs"
              >
                <Mail className="size-3.5 text-copper" />
                <span>Send Candidate Email</span>
              </Button>
            </Can>

            <Can permission="communications.create">
              <Button
                size="sm"
                variant="accent"
                onClick={handleOpenCreateTpl}
                className="gap-1 text-xs"
              >
                <Plus className="size-3.5" />
                <span>New Template</span>
              </Button>
            </Can>
          </div>
        }
      />

      {/* SMTP Status Quick Bar */}
      <div className="flex flex-wrap items-center justify-between gap-3 p-3 rounded-xs border border-border bg-card/60 text-xs">
        <div className="flex items-center gap-2.5">
          <div className={cn("size-2.5 rounded-full", smtpStatus?.isConfigured ? "bg-emerald-500 animate-pulse" : "bg-amber-500")} />
          <div>
            <span className="font-medium text-foreground">
              SMTP Outgoing Relay:{" "}
              {smtpStatus?.isConfigured ? (
                <span className="text-emerald-500 font-semibold">{smtpStatus.host}:{smtpStatus.port} ({smtpStatus.fromEmail})</span>
              ) : (
                <span className="text-amber-500 font-medium">Internal Simulated Relay (Configure SMTP in Settings)</span>
              )}
            </span>
            <span className="block text-[11px] text-muted-foreground">
              All candidate correspondence is immutably recorded in the compliance audit trail.
            </span>
          </div>
        </div>

        <Link
          href="/settings?tab=smtp"
          className="inline-flex items-center gap-1 text-[11px] font-medium text-copper hover:underline"
        >
          <Settings2 className="size-3" />
          <span>Configure SMTP Server</span>
        </Link>
      </div>

      {/* Main Tabs Header */}
      <div className="flex items-center gap-4 border-b border-border w-fit">
        <button
          type="button"
          onClick={() => handleTabChange("templates")}
          className={cn(
            "text-xs py-2 px-1 font-medium transition-all border-b-2 -mb-px whitespace-nowrap cursor-pointer",
            activeTab === "templates"
              ? "border-copper text-foreground font-semibold"
              : "border-transparent text-muted-foreground hover:text-foreground hover:border-border/60",
          )}
        >
          Email Templates ({templates.length})
        </button>
        <button
          type="button"
          onClick={() => handleTabChange("history")}
          className={cn(
            "text-xs py-2 px-1 font-medium transition-all border-b-2 -mb-px whitespace-nowrap cursor-pointer",
            activeTab === "history"
              ? "border-copper text-foreground font-semibold"
              : "border-transparent text-muted-foreground hover:text-foreground hover:border-border/60",
          )}
        >
          Delivery Audit History ({messages.length})
        </button>
      </div>

      {loading ? (
        <div className="p-12 text-center text-xs text-muted-foreground flex items-center justify-center gap-2">
          <Loader2 className="size-4 animate-spin text-copper" />
          <span>Loading communications data...</span>
        </div>
      ) : activeTab === "templates" ? (
        <div className="space-y-3">
          {/* Row 1: Full Width Template Searchbar */}
          <div className="relative w-full">
            <Search className="absolute left-3 top-2.5 size-4 text-muted-foreground" />
            <Input
              placeholder="Search all email templates by title, subject line, or trigger event..."
              value={templateSearch}
              onChange={(e) => setTemplateSearch(e.target.value)}
              className="pl-9 pr-9 h-9 text-xs bg-card w-full shadow-2xs"
            />
            {templateSearch && (
              <button
                type="button"
                onClick={() => setTemplateSearch("")}
                className="absolute right-3 top-2.5 text-xs text-muted-foreground hover:text-foreground cursor-pointer"
                title="Clear search"
              >
                ✕
              </button>
            )}
          </div>

          {/* Row 2: Stage Filter Pills & Counter */}
          <div className="flex flex-wrap items-center justify-between gap-2 pt-0.5">
            <div className="flex items-center gap-1.5 overflow-x-auto no-scrollbar pb-1 flex-wrap">
              {STAGE_FILTERS.map((st) => (
                <button
                  key={st.id}
                  type="button"
                  onClick={() => setSelectedStageFilter(st.id)}
                  className={cn(
                    "px-2.5 py-1 text-[11px] rounded-xs border transition-colors whitespace-nowrap cursor-pointer",
                    selectedStageFilter === st.id
                      ? "bg-copper/15 text-copper border-copper/50 font-semibold shadow-2xs"
                      : "border-border bg-card/60 text-muted-foreground hover:text-foreground hover:bg-muted",
                  )}
                >
                  {st.label}
                </button>
              ))}
            </div>

            <span className="text-[11px] text-muted-foreground font-medium shrink-0">
              Showing {filteredTemplates.length} of {templates.length} templates
            </span>
          </div>

          {/* Templates Grid */}
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {filteredTemplates.length === 0 ? (
              <div className="col-span-full p-12 text-center border border-dashed border-border rounded-xs text-muted-foreground text-xs">
                No templates matched your stage or search filter.
              </div>
            ) : (
              filteredTemplates.map((tpl) => (
                <Card key={tpl.id} className="shadow-none border border-border flex flex-col justify-between hover:border-copper/40 transition-colors">
                  <CardHeader className="pb-2">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0 flex-1">
                        <CardTitle className="text-sm font-semibold truncate" title={tpl.name}>
                          {tpl.name}
                        </CardTitle>
                        <CardDescription className="text-[11px] mt-0.5 flex items-center gap-1.5">
                          <span>Stage:</span>
                          <Badge variant="outline" className="text-[10px] capitalize px-1 py-0 border-copper/30 text-copper font-medium">
                            {tpl.triggerEvent}
                          </Badge>
                        </CardDescription>
                      </div>
                      <Badge variant="soft-success" className="text-[10px] shrink-0 font-medium">
                        Active
                      </Badge>
                    </div>
                  </CardHeader>
                  <CardContent className="space-y-3 text-xs flex-1 flex flex-col justify-between">
                    <div className="space-y-2">
                      <div className="p-2 bg-muted/40 rounded-xs border border-border text-[11px] text-foreground">
                        <div className="text-muted-foreground text-[9px] uppercase font-semibold">
                          Subject Line Preview
                        </div>
                        <div className="truncate font-medium mt-0.5">{tpl.subject}</div>
                      </div>

                      <div className="p-2.5 bg-card rounded-xs border border-border/80 text-[11px] text-muted-foreground line-clamp-3 leading-relaxed whitespace-pre-wrap">
                        {tpl.bodyTemplate}
                      </div>
                    </div>

                    <div className="flex items-center justify-between pt-2 border-t border-border mt-2">
                      <Button
                        size="xs"
                        variant="ghost"
                        onClick={() => handleOpenPreview(tpl)}
                        className="h-7 gap-1 text-[11px] text-copper hover:text-copper hover:bg-copper/10"
                      >
                        <Eye className="size-3" />
                        <span>Preview Simulator</span>
                      </Button>

                      <div className="flex items-center gap-1">
                        <Button
                          size="xs"
                          variant="ghost"
                          onClick={() => handleOpenEditTpl(tpl)}
                          className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                          title="Edit template"
                        >
                          <Edit2 className="size-3.5" />
                        </Button>
                        <Button
                          size="xs"
                          variant="ghost"
                          onClick={() => handleDeleteTemplate(tpl.id, tpl.name)}
                          className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                          title="Delete template"
                        >
                          <Trash2 className="size-3.5" />
                        </Button>
                      </div>
                    </div>
                  </CardContent>
                </Card>
              ))
            )}
          </div>
        </div>
      ) : (
        /* Sent Messages Log Table */
        <TableShell>
          <Table>
            <THead>
              <SortableTH
                field="candidateName"
                currentSort={historySortField === "candidateName" ? (historySortDirection === "asc" ? "candidateName" : "-candidateName") : ""}
                onSort={handleHistorySort}
              >
                Candidate Recipient
              </SortableTH>
              <TH>Subject</TH>
              <TH>Sender</TH>
              <SortableTH
                field="sentAt"
                currentSort={historySortField === "sentAt" ? (historySortDirection === "asc" ? "sentAt" : "-sentAt") : ""}
                onSort={handleHistorySort}
              >
                Dispatched Date & Time
              </SortableTH>
              <TH>Status</TH>
              <TH className="text-right">Action</TH>
            </THead>
            <TBody>
              {paginatedMessages.length === 0 ? (
                <EmptyRow colSpan={6}>No candidate message records found in the audit trail.</EmptyRow>
              ) : (
                paginatedMessages.map((msg) => (
                  <TR key={msg.id}>
                    <TD>
                      <div>
                        <span className="font-semibold text-foreground text-xs block">
                          {msg.candidateName || "Candidate"}
                        </span>
                        <span className="text-[11px] text-muted-foreground">
                          {msg.recipientEmail}
                        </span>
                      </div>
                    </TD>
                    <TD>
                      <span className="font-medium text-foreground text-xs max-w-xs truncate block">
                        {msg.subject}
                      </span>
                    </TD>
                    <TD>
                      <span className="text-xs text-muted-foreground">
                        {msg.senderName || "Recruiter"}
                      </span>
                    </TD>
                    <TD>
                      <div className="text-xs text-foreground font-medium">
                        {new Date(msg.sentAt).toLocaleDateString()}
                      </div>
                      <span className="text-[11px] text-muted-foreground">
                        {new Date(msg.sentAt).toLocaleTimeString([], {
                          hour: "2-digit",
                          minute: "2-digit",
                        })}
                      </span>
                    </TD>
                    <TD>
                      <Badge
                        variant={msg.status === "delivered" ? "soft-success" : "secondary"}
                        className="gap-1 text-[10px]"
                      >
                        <CheckCircle2 className="size-3" />
                        <span className="capitalize">{msg.status || "Delivered"}</span>
                      </Badge>
                    </TD>
                    <TD className="text-right">
                      <Button
                        size="xs"
                        variant="ghost"
                        onClick={() => {
                          setActiveViewMessage(msg);
                          setViewMessageModalOpen(true);
                        }}
                        className="gap-1 text-[11px]"
                      >
                        <Eye className="size-3" />
                        <span>View Content</span>
                      </Button>
                    </TD>
                  </TR>
                ))
              )}
            </TBody>
          </Table>

          <ClientPagination
            page={historyPage}
            limit={historyPageSize}
            total={messages.length}
            onPageChange={setHistoryPage}
            onLimitChange={setHistoryPageSize}
            limitOptions={[10, 25, 50, 100]}
          />
        </TableShell>
      )}

      {/* Live Template Preview Simulator Modal */}
      <Dialog open={previewModalOpen} onOpenChange={setPreviewModalOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold flex items-center gap-2">
              <Sparkles className="size-4 text-copper" />
              <span>Live Email Preview Simulation</span>
            </DialogTitle>
          </DialogHeader>

          {previewTemplate && (
            <div className="space-y-4 py-2 text-xs">
              <div className="flex items-center justify-between p-2.5 bg-muted/40 rounded-xs border border-border">
                <div>
                  <span className="text-[10px] uppercase text-muted-foreground font-bold">Template:</span>
                  <span className="font-semibold text-foreground ml-1.5">{previewTemplate.name}</span>
                </div>
                <Badge variant="outline" className="text-[10px] capitalize text-copper border-copper/40">
                  Trigger: {previewTemplate.triggerEvent}
                </Badge>
              </div>

              {/* Rendered Email Container */}
              <div className="p-4 rounded-xs border border-border bg-card space-y-3 font-sans">
                <div className="space-y-1 pb-3 border-b border-border text-xs">
                  <div className="flex gap-2">
                    <span className="w-16 text-muted-foreground font-medium">From:</span>
                    <span className="font-semibold text-foreground">
                      {smtpStatus?.fromName || "Karmarat Talent Team"} &lt;{smtpStatus?.fromEmail || "talent@karmarat.com"}&gt;
                    </span>
                  </div>
                  <div className="flex gap-2">
                    <span className="w-16 text-muted-foreground font-medium">To:</span>
                    <span className="text-foreground">Alex Rivera &lt;alex.rivera@example.com&gt;</span>
                  </div>
                  <div className="flex gap-2">
                    <span className="w-16 text-muted-foreground font-medium">Subject:</span>
                    <span className="font-bold text-foreground">
                      {renderEmailTemplate(previewTemplate.subject, SAMPLE_DATA)}
                    </span>
                  </div>
                </div>

                <div className="p-3 bg-background rounded-xs border border-border/60 text-xs text-foreground leading-relaxed whitespace-pre-wrap font-sans">
                  {renderEmailTemplate(previewTemplate.bodyTemplate, SAMPLE_DATA)}
                </div>

                {smtpStatus?.signature && (
                  <div className="text-[11px] text-muted-foreground border-t border-border/40 pt-2 whitespace-pre-wrap">
                    {smtpStatus.signature}
                  </div>
                )}
              </div>

              <div className="text-[11px] text-muted-foreground bg-muted/30 p-2 rounded-xs border border-border/40 flex items-center gap-1.5">
                <Check className="size-3.5 text-copper shrink-0" />
                <span>Simulated with dynamic variables: <strong>{"{{candidate_name}}"}</strong>, <strong>{"{{job_title}}"}</strong>, <strong>{"{{meeting_link}}"}</strong>, <strong>{"{{offer_salary}}"}</strong>.</span>
              </div>
            </div>
          )}

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setPreviewModalOpen(false)}>
              Close Preview
            </Button>
            <Button
              size="xs"
              variant="accent"
              onClick={() => {
                setPreviewModalOpen(false);
                if (previewTemplate) handleOpenEditTpl(previewTemplate);
              }}
              className="gap-1"
            >
              <Edit2 className="size-3" />
              <span>Edit This Template</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Create / Edit Template Modal with Token Inserter */}
      <Dialog open={templateModalOpen} onOpenChange={setTemplateModalOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">
              {editingTemplateId ? "Edit Communication Template" : "Create Communication Template"}
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="space-y-1">
                <label className="field-label">Template Name</label>
                <Input
                  value={tplName}
                  onChange={(e) => setTplName(e.target.value)}
                  placeholder="e.g. Technical Screen Invitation"
                  className="h-8 text-xs"
                />
              </div>
              <div className="space-y-1">
                <label className="field-label">Recruitment Stage Trigger</label>
                <select
                  value={tplStage}
                  onChange={(e) => setTplStage(e.target.value)}
                  className="w-full h-8 px-2 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper"
                >
                  <option value="applied">Applied (Auto-Acknowledgment)</option>
                  <option value="screening">Screening &amp; Assessment</option>
                  <option value="interview">Interview Rounds (Panel / 1-on-1)</option>
                  <option value="evaluation">Evaluation &amp; Debrief Feedback</option>
                  <option value="selected">Executive Selection &amp; Sync</option>
                  <option value="offer">Offer Extension (Verbal / Formal)</option>
                  <option value="hired">Hired &amp; Day-1 Onboarding</option>
                  <option value="rejected">Rejection with Dignity</option>
                  <option value="talent_pool">Talent Community Nurture</option>
                </select>
              </div>
            </div>

            <div className="space-y-1">
              <div className="flex items-center justify-between">
                <label className="field-label">Email Subject</label>
                <div className="flex items-center gap-1">
                  <button
                    type="button"
                    onClick={() => handleInsertSubjectToken("{{job_title}}")}
                    className="text-[10px] text-copper hover:underline font-mono"
                  >
                    + {"{{job_title}}"}
                  </button>
                  <span className="text-muted-foreground">·</span>
                  <button
                    type="button"
                    onClick={() => handleInsertSubjectToken("{{company_name}}")}
                    className="text-[10px] text-copper hover:underline font-mono"
                  >
                    + {"{{company_name}}"}
                  </button>
                </div>
              </div>
              <Input
                value={tplSubject}
                onChange={(e) => setTplSubject(e.target.value)}
                placeholder="Subject with tokens..."
                className="h-8 text-xs"
              />
            </div>

            {/* Dynamic Merge Token Palette */}
            <div className="space-y-1 p-2 bg-muted/40 rounded-xs border border-border">
              <div className="flex items-center justify-between">
                <span className="text-[10px] uppercase font-bold text-muted-foreground">
                  Available Merge Tokens (Click to append into email body)
                </span>
              </div>
              <div className="flex flex-wrap gap-1 pt-1">
                {MERGE_TOKENS.map((tk) => (
                  <button
                    key={tk.token}
                    type="button"
                    onClick={() => handleInsertToken(tk.token)}
                    title={tk.desc}
                    className="px-1.5 py-0.5 text-[10px] font-mono bg-card hover:bg-copper/10 text-foreground hover:text-copper border border-border hover:border-copper/40 rounded-xs transition-colors cursor-pointer"
                  >
                    + {tk.token}
                  </button>
                ))}
              </div>
            </div>

            <div className="space-y-1">
              <label className="field-label">Email Body Template</label>
              <Textarea
                rows={8}
                value={tplBody}
                onChange={(e) => setTplBody(e.target.value)}
                className="text-xs leading-relaxed font-sans"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setTemplateModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isSavingTpl}
              onClick={handleSaveTemplate}
              className="gap-1"
            >
              {isSavingTpl ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Template</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Send Candidate Email Modal with Template Selection */}
      <Dialog open={sendModalOpen} onOpenChange={setSendModalOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold flex items-center gap-2">
              <Mail className="size-4 text-copper" />
              <span>Direct Candidate Communication</span>
            </DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="space-y-1">
                <label className="field-label">Recipient Candidate</label>
                <select
                  value={targetCandidateId}
                  onChange={(e) => setTargetCandidateId(e.target.value)}
                  className="w-full h-8 px-2 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper"
                >
                  {candidates.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.fullName} ({c.email})
                    </option>
                  ))}
                </select>
              </div>

              <div className="space-y-1">
                <label className="field-label">Populate From Template</label>
                <select
                  value={selectedTemplateId}
                  onChange={(e) => handleTemplateSelectionForSend(e.target.value)}
                  className="w-full h-8 px-2 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper"
                >
                  <option value="">-- Custom Compose --</option>
                  {templates.map((tpl) => (
                    <option key={tpl.id} value={tpl.id}>
                      {tpl.name} ({tpl.triggerEvent})
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="space-y-1">
              <label className="field-label">Subject</label>
              <Input
                value={msgSubject}
                onChange={(e) => setMsgSubject(e.target.value)}
                className="h-8 text-xs font-medium"
              />
            </div>

            <div className="space-y-1">
              <label className="field-label">Message Content</label>
              <Textarea
                rows={7}
                value={msgBody}
                onChange={(e) => setMsgBody(e.target.value)}
                className="text-xs leading-relaxed font-sans"
              />
            </div>

            {/* Outgoing Relay Notice */}
            <div className="p-2.5 rounded-xs border border-border bg-muted/40 text-[11px] text-muted-foreground flex items-center justify-between">
              <div className="flex items-center gap-2">
                <div className={cn("size-2 rounded-full", smtpStatus?.isConfigured ? "bg-emerald-500" : "bg-amber-500")} />
                <span>
                  {smtpStatus?.isConfigured
                    ? `Will dispatch live email via SMTP relay (${smtpStatus.host}:${smtpStatus.port})`
                    : "Will log message to candidate audit trail (configure live SMTP in Settings)"}
                </span>
              </div>
              <Link href="/settings?tab=smtp" className="text-copper hover:underline">
                SMTP Settings →
              </Link>
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setSendModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isSending}
              onClick={handleSendMessage}
              className="gap-1"
            >
              {isSending ? <Loader2 className="size-3 animate-spin" /> : <Send className="size-3" />}
              <span>Send Message</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* View Message Content Modal */}
      <Dialog open={viewMessageModalOpen} onOpenChange={setViewMessageModalOpen}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold flex items-center gap-2">
              <FileText className="size-4 text-copper" />
              <span>Sent Message Details</span>
            </DialogTitle>
          </DialogHeader>

          {activeViewMessage && (
            <div className="space-y-3 py-2 text-xs">
              <div className="p-3 bg-muted/40 rounded-xs border border-border space-y-1">
                <div>
                  <span className="text-muted-foreground font-medium">To: </span>
                  <span className="font-semibold text-foreground">{activeViewMessage.recipientEmail}</span>
                </div>
                <div>
                  <span className="text-muted-foreground font-medium">Subject: </span>
                  <span className="font-semibold text-foreground">{activeViewMessage.subject}</span>
                </div>
                <div>
                  <span className="text-muted-foreground font-medium">Dispatched: </span>
                  <span className="text-foreground">{new Date(activeViewMessage.sentAt).toLocaleString()}</span>
                </div>
              </div>

              <div className="p-3 bg-card rounded-xs border border-border text-xs leading-relaxed whitespace-pre-wrap font-sans text-foreground">
                {activeViewMessage.body}
              </div>
            </div>
          )}

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setViewMessageModalOpen(false)}>
              Close
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

export function CommunicationsPage() {
  return (
    <Suspense fallback={<div className="page p-8 text-xs text-muted-foreground">Loading communications...</div>}>
      <CommunicationsContent />
    </Suspense>
  );
}
