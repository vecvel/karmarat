"use client";

import React, { useState, useEffect } from "react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  User,
  Mail,
  Phone,
  MapPin,
  Briefcase,
  Building,
  Clock,
  DollarSign,
  Calendar,
  Gift,
  XCircle,
  FileText,
  Linkedin,
  Github,
  Globe,
  Download,
  ExternalLink,
  CheckCircle2,
  AlertCircle,
  Star,
  Award,
  HelpCircle,
  MessageSquare,
  Video,
  Layers,
  ChevronRight,
  Loader2,
  X,
  Send,
  Sparkles,
} from "lucide-react";
import { toast } from "sonner";
import {
  updateApplicationStage,
  getApplicationDetails,
  type ApplicationStage,
} from "@/features/applications/server/actions";
import {
  getCommunicationTemplates,
  sendMessageToCandidate,
} from "@/features/communications/server/actions";
import { getSmtpConfig, type SmtpConfig } from "@/features/organization/server/actions";
import { renderEmailTemplate } from "@/lib/email/template-utils";
import { Can } from "@/components/rbac/can";
import { cn } from "@/lib/utils";

const STAGE_CONFIG: Record<
  string,
  { label: string; badgeVariant: "secondary" | "soft-success" | "soft-warning" | "destructive" | "default" | "outline" | "accent" | "soft-accent" }
> = {
  applied: { label: "Applied", badgeVariant: "secondary" },
  screening: { label: "Screening", badgeVariant: "soft-accent" },
  shortlisted: { label: "Shortlisted", badgeVariant: "soft-accent" },
  interview: { label: "Interview", badgeVariant: "accent" },
  evaluation: { label: "Evaluation", badgeVariant: "accent" },
  selected: { label: "Selected", badgeVariant: "soft-success" },
  offer: { label: "Offer Stage", badgeVariant: "soft-success" },
  hired: { label: "Hired (HRM)", badgeVariant: "soft-success" },
  rejected: { label: "Rejected", badgeVariant: "destructive" },
};

const ALL_STAGES: { id: ApplicationStage; name: string }[] = [
  { id: "applied", name: "Applied" },
  { id: "screening", name: "Screening" },
  { id: "shortlisted", name: "Shortlisted" },
  { id: "interview", name: "Interview" },
  { id: "evaluation", name: "Evaluation" },
  { id: "selected", name: "Selected" },
  { id: "offer", name: "Offer Stage" },
  { id: "hired", name: "Hired (HRM)" },
  { id: "rejected", name: "Rejected" },
];

const MERGE_TOKENS = [
  "{{candidate_name}}",
  "{{candidate_first_name}}",
  "{{job_title}}",
  "{{company_name}}",
  "{{meeting_link}}",
  "{{interview_date}}",
  "{{offer_salary}}",
  "{{joining_date}}",
  "{{careers_url}}",
];

interface ApplicationDetailDrawerProps {
  applicationId: string | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onStageUpdated?: (appId: string, newStage: ApplicationStage) => void;
}

export function ApplicationDetailDrawer({
  applicationId,
  open,
  onOpenChange,
  onStageUpdated,
}: ApplicationDetailDrawerProps) {
  const [app, setApp] = useState<any>(null);
  const [loading, setLoading] = useState(false);
  const [activeTab, setActiveTab] = useState<"overview" | "resume" | "questions" | "interviews">("overview");
  const [updatingStage, setUpdatingStage] = useState(false);

  // Email Composer State
  const [emailModalOpen, setEmailModalOpen] = useState(false);
  const [templates, setTemplates] = useState<any[]>([]);
  const [selectedTemplateId, setSelectedTemplateId] = useState("");
  const [emailSubject, setEmailSubject] = useState("");
  const [emailBody, setEmailBody] = useState("");
  const [isSendingEmail, setIsSendingEmail] = useState(false);
  const [smtpConfig, setSmtpConfig] = useState<SmtpConfig | null>(null);

  useEffect(() => {
    if (!applicationId || !open) return;

    async function load() {
      setLoading(true);
      try {
        const [data, tpls, smtp] = await Promise.all([
          getApplicationDetails(applicationId!),
          getCommunicationTemplates(),
          getSmtpConfig(),
        ]);
        setApp(data);
        setTemplates(tpls);
        setSmtpConfig(smtp);
      } catch (err) {
        console.error("Failed to load application details:", err);
        toast.error("Failed to load application details");
      } finally {
        setLoading(false);
      }
    }
    load();
  }, [applicationId, open]);

  const handleStageChange = async (newStage: ApplicationStage) => {
    if (!app) return;
    setUpdatingStage(true);
    try {
      await updateApplicationStage(app.id, newStage);
      setApp((prev: any) => ({ ...prev, stage: newStage }));
      onStageUpdated?.(app.id, newStage);
      toast.success(`Application advanced to ${newStage.toUpperCase()}`);
    } catch {
      toast.error("Failed to update application stage");
    } finally {
      setUpdatingStage(false);
    }
  };

  const getCandidateVariables = () => {
    const candidateName = app?.candidateName || "Candidate";
    const firstName = candidateName.split(" ")[0] || "Candidate";
    return {
      candidate_name: candidateName,
      candidate_first_name: firstName,
      job_title: app?.jobTitle || "Job Opening",
      req_code: app?.reqCode || "REQ",
      department: app?.departmentName || "General",
      company_name: "Karmarat",
      work_location: app?.locationText || "Remote Hub",
      interview_date: "Upcoming Week",
      interview_time: "2:00 PM EST",
      meeting_link: "https://meet.google.com/rqb-recruitment",
      interviewer_name: "Hiring Manager",
      offer_salary: app?.expectedSalary ? `$${Number(app.expectedSalary).toLocaleString()} / year` : "$175,000 / year",
      joining_date: "Within 30 Days",
      sender_name: "Karmarat Talent Team",
      careers_url: "https://karmarat.com/careers",
    };
  };

  const handleOpenEmailComposer = () => {
    if (!app) return;
    const stage = app.stage || "applied";

    // Auto-match the template matching the candidate's current stage
    let matchedTpl = templates.find((t) => t.triggerEvent === stage);
    if (!matchedTpl && stage === "shortlisted") {
      matchedTpl = templates.find((t) => t.triggerEvent === "screening");
    }
    if (!matchedTpl && templates.length > 0) {
      matchedTpl = templates[0];
    }

    const vars = getCandidateVariables();

    if (matchedTpl) {
      setSelectedTemplateId(matchedTpl.id);
      setEmailSubject(renderEmailTemplate(matchedTpl.subject, vars));
      setEmailBody(renderEmailTemplate(matchedTpl.bodyTemplate, vars));
    } else {
      setSelectedTemplateId("");
      setEmailSubject(`Update regarding your application for ${app.jobTitle} at Karmarat`);
      setEmailBody(`Hi ${vars.candidate_first_name},\n\nWe wanted to share an update regarding your application for ${app.jobTitle}...\n\nBest regards,\nKarmarat Talent Team`);
    }

    setEmailModalOpen(true);
  };

  const handleSelectTemplateInComposer = (tplId: string) => {
    setSelectedTemplateId(tplId);
    if (!tplId) return;

    const tpl = templates.find((t) => t.id === tplId);
    if (!tpl) return;

    const vars = getCandidateVariables();
    setEmailSubject(renderEmailTemplate(tpl.subject, vars));
    setEmailBody(renderEmailTemplate(tpl.bodyTemplate, vars));
  };

  const handleInsertTokenIntoBody = (token: string) => {
    setEmailBody((prev) => prev + " " + token);
  };

  const handleSendEmail = async () => {
    if (!app || !emailSubject.trim() || !emailBody.trim()) {
      toast.error("Please fill in email subject and message body");
      return;
    }

    setIsSendingEmail(true);
    try {
      const res = await sendMessageToCandidate({
        candidateId: app.candidateId,
        templateId: selectedTemplateId || undefined,
        recipientEmail: app.candidateEmail,
        subject: emailSubject.trim(),
        body: emailBody.trim(),
      });

      if (res.sentViaSmtp) {
        toast.success(`Live email dispatched via SMTP to ${app.candidateEmail}!`);
      } else {
        toast.success(`Message recorded in candidate audit trail.`);
      }

      setEmailModalOpen(false);
    } catch (err: any) {
      toast.error(err.message || "Failed to send email");
    } finally {
      setIsSendingEmail(false);
    }
  };

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="sm:max-w-4xl max-h-[90vh] flex flex-col p-0 gap-0 overflow-hidden border border-border shadow-2xl">
          {/* Header Banner */}
          {loading || !app ? (
            <div className="p-16 flex flex-col items-center justify-center gap-3 text-muted-foreground text-xs">
              <Loader2 className="size-6 animate-spin text-copper" />
              <span>Loading full candidate application record...</span>
            </div>
          ) : (
            <>
              <div className="p-5 border-b border-border bg-muted/30 space-y-4">
                <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
                  <div className="space-y-1">
                    <div className="flex items-center gap-2 flex-wrap">
                      <h2 className="text-lg font-bold text-foreground tracking-tight">
                        {app.candidateName}
                      </h2>
                      <Badge
                        variant={STAGE_CONFIG[app.stage]?.badgeVariant || "secondary"}
                        className="text-[11px] capitalize font-medium"
                      >
                        {STAGE_CONFIG[app.stage]?.label || app.stage}
                      </Badge>
                      {app.matchScore && (
                        <Badge variant="outline" className="text-[11px] border-copper/40 text-copper flex items-center gap-1">
                          <Star className="size-3 fill-copper text-copper" />
                          <span>{app.matchScore}% Fit Score</span>
                        </Badge>
                      )}
                    </div>
                    <div className="text-xs text-muted-foreground flex items-center gap-2 flex-wrap">
                      <span className="font-medium text-foreground">{app.jobTitle}</span>
                      {app.reqCode && (
                        <span className="bg-muted px-1.5 py-0.5 rounded-xs font-mono text-[10px] text-muted-foreground">
                          {app.reqCode}
                        </span>
                      )}
                      <span>•</span>
                      <span>Applied {new Date(app.createdAt).toLocaleDateString()}</span>
                    </div>
                  </div>

                  {/* Quick Stage Progression & Action Buttons */}
                  <div className="flex items-center gap-2 shrink-0 flex-wrap">
                    <Can permission="applications.advance_stage">
                      <div className="flex items-center gap-1.5 bg-muted/60 px-2 py-1 rounded-xs border border-border">
                        <span className="text-[11px] text-muted-foreground font-medium">Stage:</span>
                        <select
                          value={app.stage}
                          disabled={updatingStage}
                          onChange={(e) => handleStageChange(e.target.value as ApplicationStage)}
                          className="h-6 text-xs bg-card border border-border rounded-xs px-2 text-foreground font-semibold cursor-pointer focus:ring-1 focus:ring-copper"
                        >
                          {ALL_STAGES.map((s) => (
                            <option key={s.id} value={s.id}>
                              {s.name}
                            </option>
                          ))}
                        </select>
                      </div>
                    </Can>

                    <Can permission="communications.send">
                      <Button
                        size="xs"
                        variant="outline"
                        onClick={handleOpenEmailComposer}
                        className="gap-1 text-xs text-copper border-copper/40 hover:bg-copper/10"
                      >
                        <Mail className="size-3" />
                        <span>Email Candidate</span>
                      </Button>
                    </Can>

                    <Can permission="interviews.create">
                      <Link href={`/interviews/schedule?candidateId=${app.candidateId}&applicationId=${app.id}`}>
                        <Button size="xs" variant="outline" className="gap-1 text-xs">
                          <Calendar className="size-3 text-copper" />
                          <span>Schedule</span>
                        </Button>
                      </Link>
                    </Can>

                    <Can permission="offers.create">
                      <Link href={`/offers/new?candidateId=${app.candidateId}&applicationId=${app.id}`}>
                        <Button size="xs" variant="accent" className="gap-1 text-xs">
                          <Gift className="size-3" />
                          <span>Generate Offer</span>
                        </Button>
                      </Link>
                    </Can>
                  </div>
                </div>

                {/* Navigation Tabs */}
                <div className="flex items-center gap-4 border-b border-border/80 -mb-5 pt-1 overflow-x-auto no-scrollbar">
                  <button
                    type="button"
                    onClick={() => setActiveTab("overview")}
                    className={cn(
                      "text-xs py-2 px-1 font-medium transition-all border-b-2 -mb-px cursor-pointer whitespace-nowrap",
                      activeTab === "overview"
                        ? "border-copper text-foreground font-semibold"
                        : "border-transparent text-muted-foreground hover:text-foreground",
                    )}
                  >
                    Candidate Profile &amp; Metrics
                  </button>
                  <button
                    type="button"
                    onClick={() => setActiveTab("resume")}
                    className={cn(
                      "text-xs py-2 px-1 font-medium transition-all border-b-2 -mb-px flex items-center gap-1.5 cursor-pointer whitespace-nowrap",
                      activeTab === "resume"
                        ? "border-copper text-foreground font-semibold"
                        : "border-transparent text-muted-foreground hover:text-foreground",
                    )}
                  >
                    <FileText className="size-3 text-copper" />
                    <span>Resume &amp; Cover Letter</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => setActiveTab("questions")}
                    className={cn(
                      "text-xs py-2 px-1 font-medium transition-all border-b-2 -mb-px flex items-center gap-1.5 cursor-pointer whitespace-nowrap",
                      activeTab === "questions"
                        ? "border-copper text-foreground font-semibold"
                        : "border-transparent text-muted-foreground hover:text-foreground",
                    )}
                  >
                    <HelpCircle className="size-3 text-copper" />
                    <span>Custom Screening Q&amp;A</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => setActiveTab("interviews")}
                    className={cn(
                      "text-xs py-2 px-1 font-medium transition-all border-b-2 -mb-px flex items-center gap-1.5 cursor-pointer whitespace-nowrap",
                      activeTab === "interviews"
                        ? "border-copper text-foreground font-semibold"
                        : "border-transparent text-muted-foreground hover:text-foreground",
                    )}
                  >
                    <Video className="size-3 text-copper" />
                    <span>Interview Rounds ({app.interviews?.length || 0})</span>
                  </button>
                </div>
              </div>

              {/* Drawer Tab Viewport Body */}
              <div className="flex-1 overflow-y-auto p-5 text-xs space-y-5">
                {activeTab === "overview" && (
                  <div className="space-y-5">
                    {/* Key Hiring Metrics Grid */}
                    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                      <div className="p-3 bg-muted/40 rounded-xs border border-border space-y-0.5">
                        <span className="text-[10px] uppercase font-bold text-muted-foreground block">
                          Total Experience
                        </span>
                        <span className="text-sm font-semibold text-foreground">
                          {app.experienceYears !== undefined && app.experienceYears !== null
                            ? `${app.experienceYears} Years`
                            : "5+ Years"}
                        </span>
                      </div>

                      <div className="p-3 bg-muted/40 rounded-xs border border-border space-y-0.5">
                        <span className="text-[10px] uppercase font-bold text-muted-foreground block">
                          Notice Period
                        </span>
                        <span className="text-sm font-semibold text-foreground">
                          {app.noticePeriodDays !== undefined && app.noticePeriodDays !== null
                            ? `${app.noticePeriodDays} Days`
                            : "30 Days"}
                        </span>
                      </div>

                      <div className="p-3 bg-muted/40 rounded-xs border border-border space-y-0.5">
                        <span className="text-[10px] uppercase font-bold text-muted-foreground block">
                          Expected Compensation
                        </span>
                        <span className="text-sm font-semibold text-foreground">
                          {app.expectedSalary
                            ? `$${Number(app.expectedSalary).toLocaleString()} / yr`
                            : "Competitive"}
                        </span>
                      </div>

                      <div className="p-3 bg-muted/40 rounded-xs border border-border space-y-0.5">
                        <span className="text-[10px] uppercase font-bold text-muted-foreground block">
                          Current Designation
                        </span>
                        <span className="text-sm font-semibold text-foreground truncate block">
                          {app.currentRole || "Senior Engineer"}
                        </span>
                      </div>
                    </div>

                    {/* Contact & Location Details Card */}
                    <Card className="p-4 shadow-none border border-border space-y-3">
                      <h3 className="text-xs font-bold uppercase tracking-wider text-muted-foreground">
                        Candidate Contact &amp; Professional Links
                      </h3>

                      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-3">
                        <div className="flex items-center justify-between p-2 rounded-xs bg-card border border-border">
                          <div className="flex items-center gap-2 truncate">
                            <Mail className="size-3.5 text-copper shrink-0" />
                            <span className="text-foreground truncate">{app.candidateEmail}</span>
                          </div>
                          <button
                            type="button"
                            onClick={handleOpenEmailComposer}
                            className="text-[10px] text-copper hover:underline font-semibold shrink-0 ml-1"
                          >
                            Send Email
                          </button>
                        </div>

                        <div className="flex items-center gap-2 p-2 rounded-xs bg-card border border-border truncate">
                          <Phone className="size-3.5 text-copper shrink-0" />
                          <span className="text-foreground truncate">{app.candidatePhone || "+1 (555) 234-5678"}</span>
                        </div>

                        <div className="flex items-center gap-2 p-2 rounded-xs bg-card border border-border truncate">
                          <MapPin className="size-3.5 text-copper shrink-0" />
                          <span className="text-foreground truncate">{app.candidateCity || "San Francisco"}, {app.candidateCountry || "United States"}</span>
                        </div>
                      </div>

                      {/* External Web Links */}
                      <div className="flex items-center gap-2 pt-1 flex-wrap">
                        {app.linkedinUrl && (
                          <a
                            href={app.linkedinUrl}
                            target="_blank"
                            rel="noreferrer"
                            className="inline-flex items-center gap-1 text-[11px] px-2.5 py-1 bg-muted/60 hover:bg-muted text-foreground border border-border rounded-xs transition-colors"
                          >
                            <Linkedin className="size-3 text-copper" />
                            <span>LinkedIn Profile</span>
                            <ExternalLink className="size-2.5 text-muted-foreground ml-0.5" />
                          </a>
                        )}

                        {app.githubUrl && (
                          <a
                            href={app.githubUrl}
                            target="_blank"
                            rel="noreferrer"
                            className="inline-flex items-center gap-1 text-[11px] px-2.5 py-1 bg-muted/60 hover:bg-muted text-foreground border border-border rounded-xs transition-colors"
                          >
                            <Github className="size-3 text-copper" />
                            <span>GitHub Portfolio</span>
                            <ExternalLink className="size-2.5 text-muted-foreground ml-0.5" />
                          </a>
                        )}

                        {app.portfolioUrl && (
                          <a
                            href={app.portfolioUrl}
                            target="_blank"
                            rel="noreferrer"
                            className="inline-flex items-center gap-1 text-[11px] px-2.5 py-1 bg-muted/60 hover:bg-muted text-foreground border border-border rounded-xs transition-colors"
                          >
                            <Globe className="size-3 text-copper" />
                            <span>Personal Website</span>
                            <ExternalLink className="size-2.5 text-muted-foreground ml-0.5" />
                          </a>
                        )}
                      </div>
                    </Card>

                    {/* Skill Tags */}
                    {app.skills && Array.isArray(app.skills) && app.skills.length > 0 && (
                      <Card className="p-4 shadow-none border border-border space-y-2">
                        <h3 className="text-xs font-bold uppercase tracking-wider text-muted-foreground">
                          Tagged Candidate Skills &amp; Competencies
                        </h3>
                        <div className="flex flex-wrap gap-1.5">
                          {app.skills.map((sk: string, i: number) => (
                            <Badge key={i} variant="outline" className="text-xs font-normal border-border bg-muted/40">
                              {sk}
                            </Badge>
                          ))}
                        </div>
                      </Card>
                    )}
                  </div>
                )}

                {activeTab === "resume" && (
                  <div className="space-y-4">
                    {/* Resume Header / Download Toolbar */}
                    <div className="flex items-center justify-between p-3 bg-muted/40 rounded-xs border border-border">
                      <div className="flex items-center gap-2">
                        <FileText className="size-4 text-copper" />
                        <div>
                          <span className="font-semibold text-foreground block text-xs">
                            {app.candidateName}&apos;s Official Resume
                          </span>
                          <span className="text-[10px] text-muted-foreground">
                            {app.resumeUrl ? "PDF / DOCX Application Document" : "Standardized Candidate Submission"}
                          </span>
                        </div>
                      </div>

                      {app.resumeUrl ? (
                        <div className="flex items-center gap-2">
                          <a
                            href={app.resumeUrl}
                            target="_blank"
                            rel="noreferrer"
                            className="inline-flex items-center gap-1 text-xs text-copper hover:underline font-medium"
                          >
                            <ExternalLink className="size-3.5" />
                            <span>Open in Tab</span>
                          </a>
                          <a
                            href={app.resumeUrl}
                            download
                            className="inline-flex items-center gap-1 px-2.5 py-1 text-xs rounded-xs bg-accent text-accent-foreground font-semibold hover:bg-accent/90"
                          >
                            <Download className="size-3" />
                            <span>Download Resume</span>
                          </a>
                        </div>
                      ) : null}
                    </div>

                    {/* PDF Viewer / Document Preview Box */}
                    {app.resumeUrl ? (
                      <div className="w-full h-[480px] rounded-xs border border-border overflow-hidden bg-muted/20">
                        <iframe
                          src={app.resumeUrl}
                          className="w-full h-full border-none"
                          title={`${app.candidateName} Resume`}
                        />
                      </div>
                    ) : (
                      <div className="p-8 text-center border border-dashed border-border rounded-xs space-y-2 text-muted-foreground">
                        <FileText className="size-8 text-muted-foreground mx-auto" />
                        <p className="font-medium text-foreground">No direct resume attachment URL available</p>
                        <p className="text-[11px]">Candidate submitted application details directly via Careers Portal.</p>
                      </div>
                    )}

                    {/* Cover Letter Section */}
                    {app.coverLetter && (
                      <Card className="p-4 shadow-none border border-border space-y-2">
                        <h3 className="text-xs font-bold uppercase tracking-wider text-muted-foreground">
                          Candidate Cover Letter / Submission Note
                        </h3>
                        <div className="p-3 bg-card rounded-xs border border-border/70 text-xs leading-relaxed whitespace-pre-wrap text-foreground">
                          {app.coverLetter}
                        </div>
                      </Card>
                    )}
                  </div>
                )}

                {activeTab === "questions" && (
                  <div className="space-y-4">
                    <div className="p-3 bg-muted/40 rounded-xs border border-border flex items-center justify-between">
                      <div>
                        <span className="font-semibold text-foreground block text-xs">
                          Requisition Screening Questionnaire
                        </span>
                        <span className="text-[10px] text-muted-foreground">
                          Candidate responses recorded during application submission
                        </span>
                      </div>
                    </div>

                    {app.screeningAnswers && Object.keys(app.screeningAnswers).length > 0 ? (
                      <div className="space-y-3">
                        {Object.entries(app.screeningAnswers).map(([question, answer]: [string, any], idx) => (
                          <Card key={idx} className="p-3.5 shadow-none border border-border space-y-1.5">
                            <div className="flex items-start gap-2">
                              <span className="size-4 rounded-full bg-copper/10 text-copper font-bold text-[10px] flex items-center justify-center shrink-0 mt-0.5">
                                {idx + 1}
                              </span>
                              <span className="font-semibold text-foreground text-xs">{question}</span>
                            </div>
                            <div className="pl-6 text-xs text-muted-foreground leading-relaxed">
                              {typeof answer === "boolean"
                                ? answer ? "Yes" : "No"
                                : Array.isArray(answer)
                                ? answer.join(", ")
                                : String(answer || "No response provided")}
                            </div>
                          </Card>
                        ))}
                      </div>
                    ) : (
                      <div className="p-8 text-center border border-dashed border-border rounded-xs text-muted-foreground space-y-1">
                        <p className="font-medium text-foreground">No custom screening questions recorded</p>
                        <p className="text-[11px]">This requisition used standard profile and resume submission fields.</p>
                      </div>
                    )}
                  </div>
                )}

                {activeTab === "interviews" && (
                  <div className="space-y-4">
                    <div className="flex items-center justify-between p-3 bg-muted/40 rounded-xs border border-border">
                      <div>
                        <span className="font-semibold text-foreground block text-xs">
                          Scheduled Interview Rounds &amp; Panel Logs
                        </span>
                        <span className="text-[10px] text-muted-foreground">
                          Live evaluations for {app.candidateName}
                        </span>
                      </div>

                      <Can permission="interviews.create">
                        <Link href={`/interviews/schedule?candidateId=${app.candidateId}&applicationId=${app.id}`}>
                          <Button size="xs" variant="accent" className="gap-1 text-xs">
                            <Calendar className="size-3" />
                            <span>+ Book Round</span>
                          </Button>
                        </Link>
                      </Can>
                    </div>

                    {app.interviews && app.interviews.length > 0 ? (
                      <div className="space-y-3">
                        {app.interviews.map((iv: any) => (
                          <Card key={iv.id} className="p-3.5 shadow-none border border-border space-y-2">
                            <div className="flex items-start justify-between gap-2">
                              <div>
                                <h4 className="font-semibold text-foreground text-xs">
                                  {iv.roundTitle || "Interview Round"}
                                </h4>
                                <span className="text-[10px] text-muted-foreground capitalize">
                                  Format: {iv.roundType || "Technical"}
                                </span>
                              </div>
                              <Badge
                                variant={iv.status === "completed" ? "soft-success" : iv.status === "cancelled" ? "destructive" : "secondary"}
                                className="text-[10px] capitalize"
                              >
                                {iv.status}
                              </Badge>
                            </div>

                            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 text-xs text-muted-foreground pt-1 border-t border-border/40">
                              <div>
                                <span className="text-[10px] uppercase block">Scheduled Date</span>
                                <span className="text-foreground font-medium">
                                  {new Date(iv.scheduledStart).toLocaleDateString()}
                                </span>
                              </div>
                              <div>
                                <span className="text-[10px] uppercase block">Duration</span>
                                <span className="text-foreground">{iv.durationMinutes || 45} Minutes</span>
                              </div>
                              <div>
                                <span className="text-[10px] uppercase block">Format</span>
                                <span className="text-foreground capitalize">{iv.format || "Video"}</span>
                              </div>
                              <div>
                                <span className="text-[10px] uppercase block">Meeting Link</span>
                                {iv.meetingLink ? (
                                  <a
                                    href={iv.meetingLink}
                                    target="_blank"
                                    rel="noreferrer"
                                    className="text-copper hover:underline truncate block"
                                  >
                                    Join Video Call
                                  </a>
                                ) : (
                                  <span>—</span>
                                )}
                              </div>
                            </div>
                          </Card>
                        ))}
                      </div>
                    ) : (
                      <div className="p-8 text-center border border-dashed border-border rounded-xs text-muted-foreground space-y-1">
                        <p className="font-medium text-foreground">No interview rounds scheduled yet</p>
                        <p className="text-[11px]">Use the &quot;Book Round&quot; button above to schedule a panel interview.</p>
                      </div>
                    )}
                  </div>
                )}
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>

      {/* Stage-Aware Candidate Email Composer Modal */}
      <Dialog open={emailModalOpen} onOpenChange={setEmailModalOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold flex items-center gap-2">
              <Mail className="size-4 text-copper" />
              <span>Email Candidate · {app?.candidateName}</span>
            </DialogTitle>
          </DialogHeader>

          {app && (
            <div className="space-y-3 py-2 text-xs">
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div className="space-y-1">
                  <label className="field-label">Recipient</label>
                  <Input
                    value={`${app.candidateName} <${app.candidateEmail}>`}
                    disabled
                    className="h-8 text-xs bg-muted/50 font-medium"
                  />
                </div>

                <div className="space-y-1">
                  <label className="field-label">Template (Stage-Matched)</label>
                  <select
                    value={selectedTemplateId}
                    onChange={(e) => handleSelectTemplateInComposer(e.target.value)}
                    className="w-full h-8 px-2 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper"
                  >
                    <option value="">-- Custom Message --</option>
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
                  value={emailSubject}
                  onChange={(e) => setEmailSubject(e.target.value)}
                  className="h-8 text-xs font-semibold"
                />
              </div>

              {/* Dynamic Tokens Palette */}
              <div className="space-y-1 p-2 bg-muted/40 rounded-xs border border-border">
                <span className="text-[10px] uppercase font-bold text-muted-foreground block">
                  Quick Insert Tokens
                </span>
                <div className="flex flex-wrap gap-1 pt-0.5">
                  {MERGE_TOKENS.map((token) => (
                    <button
                      key={token}
                      type="button"
                      onClick={() => handleInsertTokenIntoBody(token)}
                      className="px-1.5 py-0.5 text-[10px] font-mono bg-card hover:bg-copper/10 text-foreground hover:text-copper border border-border rounded-xs transition-colors cursor-pointer"
                    >
                      + {token}
                    </button>
                  ))}
                </div>
              </div>

              <div className="space-y-1">
                <label className="field-label">Message Content</label>
                <Textarea
                  rows={8}
                  value={emailBody}
                  onChange={(e) => setEmailBody(e.target.value)}
                  className="text-xs leading-relaxed font-sans"
                />
              </div>

              {/* SMTP Status Notice */}
              <div className="p-2 rounded-xs border border-border bg-muted/30 text-[11px] text-muted-foreground flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <div className={cn("size-2 rounded-full", smtpConfig?.isConfigured ? "bg-emerald-500" : "bg-amber-500")} />
                  <span>
                    {smtpConfig?.isConfigured
                      ? `Will send live via SMTP (${smtpConfig.host}:${smtpConfig.port})`
                      : "Simulation mode (logged in audit trail)"}
                  </span>
                </div>
                <Link href="/settings?tab=smtp" className="text-copper hover:underline text-[10px]">
                  Configure SMTP →
                </Link>
              </div>
            </div>
          )}

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEmailModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isSendingEmail}
              onClick={handleSendEmail}
              className="gap-1.5 text-xs font-semibold"
            >
              {isSendingEmail ? <Loader2 className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
              <span>Send Candidate Email</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
