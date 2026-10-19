"use client";

import React, { useState, useEffect, Suspense } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@/components/ui/card";
import { PageHeader } from "@/components/shared/page-header";
import {
  FileCheck,
  Building2,
  Calendar,
  DollarSign,
  CheckCircle2,
  ArrowLeft,
  Loader2,
  Plus,
  Trash2,
  FileText,
  User,
  MapPin,
  Clock,
  Sparkles,
  Award,
  Layers,
  Printer,
  Eye,
  ShieldCheck,
  HelpCircle,
} from "lucide-react";
import { toast } from "sonner";
import { getApplications } from "@/features/applications/server/actions";
import { createOffer } from "@/features/offers/server/actions";
import {
  getCurrencies,
  getPayFrequencies,
} from "@/features/masters/server/actions";
import { cn } from "@/lib/utils";

const OFFER_TEMPLATES = [
  {
    id: "standard",
    name: "Standard Full-Time",
    desc: "Standard corporate package with base salary, annual bonus, and standard benefits.",
    defaultBenefits: "Comprehensive Medical, Dental, Vision, 401(k) with 4% employer match, 20 days paid annual leave.",
    defaultProbation: "90 Days",
    defaultNotice: "30 Days",
  },
  {
    id: "executive",
    name: "Executive / Leadership",
    desc: "Senior management package with executive compensation, equity, retention milestones, and relocation.",
    defaultBenefits: "Executive Healthcare Suite, 401(k) 6% Match, $10,000 Annual Executive Coaching & Wellness Stipend, Unlimited PTO.",
    defaultProbation: "6 Months",
    defaultNotice: "90 Days",
  },
  {
    id: "tech_equity",
    name: "Engineering (Equity Heavy)",
    desc: "High-growth tech tier with substantial ISO/RSU equity grants, home lab budget, and flexible remote terms.",
    defaultBenefits: "Full Medical/Dental/Vision coverage, $3,500 Annual Learning Budget, $1,500 Home Office Setup, 401(k) Match.",
    defaultProbation: "90 Days",
    defaultNotice: "30 Days",
  },
  {
    id: "contractor",
    name: "Fixed-Term Contractor",
    desc: "Time-bound consulting agreement with hourly/monthly rate and milestone deliverables.",
    defaultBenefits: "Equipment provision and designated expense reimbursement as outlined in master services statement.",
    defaultProbation: "30 Days",
    defaultNotice: "14 Days",
  },
  {
    id: "remote_intl",
    name: "International Remote (EOR)",
    desc: "Global employment package compliant with local jurisdiction labor laws and statutory benefits.",
    defaultBenefits: "Statutory Local Health Insurance, Pension/Provident Fund Match, Co-working Space Allowance, Global Offsites.",
    defaultProbation: "90 Days",
    defaultNotice: "30 Days",
  },
];

const PREMADE_CUSTOM_PROVISIONS = [
  { key: "Visa Sponsorship", value: "Full O-1 / H-1B transfer petition and legal fees covered upon employment commencement." },
  { key: "Hardware & Workspace Budget", value: "Top-spec 16\" MacBook Pro M4 Max, dual 4K Displays, and $1,000 ergonomic chair allowance." },
  { key: "Relocation Assistance", value: "$10,000 lump sum relocation stipend paid on first standard payroll cycle." },
  { key: "Milestone Retention Bonus", value: "$15,000 retention milestone bonus payable upon completion of 12 months continuous service." },
  { key: "Travel & Engineering Offsites", value: "Quarterly all-hands offsite flights, lodging, and meals fully covered by company." },
  { key: "Continuing Education", value: "$3,000 annual budget for conferences, certifications, and technical books." },
];

function CreateOfferContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const paramCandidateId = searchParams.get("candidateId");
  const paramApplicationId = searchParams.get("applicationId");

  const [applications, setApplications] = useState<any[]>([]);
  const [currenciesList, setCurrenciesList] = useState<any[]>([]);
  const [payFrequenciesList, setPayFrequenciesList] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [viewMode, setViewMode] = useState<"builder" | "preview">("builder");

  // Offer Configuration State
  const [templateType, setTemplateType] = useState("standard");
  const [selectedAppId, setSelectedAppId] = useState(paramApplicationId || "");
  const [designation, setDesignation] = useState("Staff Software Engineer");
  const [gradeLevel, setGradeLevel] = useState("IC5 / Senior Staff");
  const [departmentName, setDepartmentName] = useState("Engineering");
  const [currency, setCurrency] = useState("USD");
  const [payFrequency, setPayFrequency] = useState("annual");
  const [baseSalary, setBaseSalary] = useState("175000");
  const [signOnBonus, setSignOnBonus] = useState("15000");
  const [annualBonus, setAnnualBonus] = useState("15% Target Performance Bonus (Paid Annually)");
  const [equityShares, setEquityShares] = useState("12,500 Incentive Stock Options (ISO), 4-Year Vesting with 1-Year Cliff");
  const [joiningDate, setJoiningDate] = useState("2026-09-15");
  const [expiresAt, setExpiresAt] = useState("2026-09-01");
  const [reportingManager, setReportingManager] = useState("Engineering Director");
  const [workLocation, setWorkLocation] = useState("San Francisco HQ / Hybrid (2 Days Remote)");
  const [probationPeriod, setProbationPeriod] = useState("90 Days");
  const [noticePeriod, setNoticePeriod] = useState("30 Days");
  const [benefits, setBenefits] = useState(
    "Comprehensive Medical, Dental, Vision, 401(k) with 4% employer match, $3,000 Annual Learning Budget, 20 Days Paid Annual Leave",
  );

  // Dynamic Custom Fields / Provisions Builder
  const [customFields, setCustomFields] = useState<Array<{ key: string; value: string }>>([
    { key: "Hardware & Workspace Budget", value: "Top-spec 16\" MacBook Pro M4 Max, dual 4K Displays, and $1,000 ergonomic chair allowance." },
    { key: "Continuing Education", value: "$3,000 annual budget for conferences, certifications, and technical books." },
  ]);

  useEffect(() => {
    async function load() {
      try {
        const [apps, currList, freqList] = await Promise.all([
          getApplications(),
          getCurrencies(),
          getPayFrequencies(),
        ]);
        setApplications(apps);
        setCurrenciesList(currList);
        setPayFrequenciesList(freqList);

        if (currList[0]) setCurrency(currList[0].code);
        if (freqList[0]) setPayFrequency(freqList[0].name.toLowerCase());

        let targetApp: any = null;
        if (paramApplicationId) {
          targetApp = apps.find((a) => a.id === paramApplicationId);
        } else if (paramCandidateId) {
          targetApp = apps.find((a) => a.candidateId === paramCandidateId);
        } else if (apps[0]) {
          targetApp = apps[0];
        }

        if (targetApp) {
          setSelectedAppId(targetApp.id);
          setDesignation(targetApp.jobTitle || "Engineer");
          setDepartmentName(targetApp.departmentName || "Engineering");
          if (targetApp.expectedSalary) {
            setBaseSalary(String(targetApp.expectedSalary));
          }
          if (targetApp.locationText) {
            setWorkLocation(`${targetApp.locationText} / ${targetApp.workMode || "Hybrid"}`);
          }
        }
      } catch (err) {
        console.error(err);
      } finally {
        setLoading(false);
      }
    }
    load();
  }, [paramApplicationId, paramCandidateId]);

  const handleAppSelect = (appId: string) => {
    setSelectedAppId(appId);
    const found = applications.find((a) => a.id === appId);
    if (found) {
      setDesignation(found.jobTitle || "Engineer");
      setDepartmentName(found.departmentName || "Engineering");
      if (found.expectedSalary) {
        setBaseSalary(String(found.expectedSalary));
      }
      if (found.locationText) {
        setWorkLocation(`${found.locationText} / ${found.workMode || "Hybrid"}`);
      }
    }
  };

  const handleTemplateChange = (templateId: string) => {
    setTemplateType(templateId);
    const t = OFFER_TEMPLATES.find((tpl) => tpl.id === templateId);
    if (t) {
      setBenefits(t.defaultBenefits);
      setProbationPeriod(t.defaultProbation);
      setNoticePeriod(t.defaultNotice);
      if (templateId === "executive") {
        setGradeLevel("VP / Executive");
        setAnnualBonus("30% Target Executive Bonus + Equity Multiplier");
        setEquityShares("50,000 Stock Units / 4-Year Vesting with Accelerated Double-Trigger");
      } else if (templateId === "contractor") {
        setPayFrequency("monthly");
        setAnnualBonus("N/A (Fixed Contractor Rate)");
        setEquityShares("N/A");
      } else if (templateId === "tech_equity") {
        setEquityShares("20,000 Incentive Stock Options (ISO) / 4-Year Vesting");
      }
    }
  };

  const addCustomField = () => {
    setCustomFields((prev) => [...prev, { key: "Special Provision", value: "" }]);
  };

  const removeCustomField = (index: number) => {
    setCustomFields((prev) => prev.filter((_, i) => i !== index));
  };

  const updateCustomField = (index: number, field: "key" | "value", val: string) => {
    setCustomFields((prev) =>
      prev.map((item, i) => (i === index ? { ...item, [field]: val } : item)),
    );
  };

  const addPremadeProvision = (provision: { key: string; value: string }) => {
    if (customFields.some((f) => f.key === provision.key)) {
      toast.info(`"${provision.key}" is already added.`);
      return;
    }
    setCustomFields((prev) => [...prev, provision]);
    toast.success(`Added ${provision.key} clause.`);
  };

  const selectedApp = applications.find((a) => a.id === selectedAppId);

  // Generate Letter Document Content
  const generateLetterContent = () => {
    const candidateName = selectedApp?.candidateName || "Candidate";
    const curSymbol = currency === "USD" ? "$" : currency === "EUR" ? "€" : currency === "GBP" ? "£" : currency === "INR" ? "₹" : `${currency} `;
    const formattedSalary = `${curSymbol}${parseInt(baseSalary || "0").toLocaleString()} (${payFrequency.toUpperCase()})`;

    return `FORMAL OFFER OF EMPLOYMENT
Karmarat Enterprise Talent Systems · Org: My Organisation

Date: ${new Date().toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric" })}

To:
${candidateName}
${selectedApp?.candidateEmail || "candidate@email.com"}
${selectedApp?.candidateCity || "San Francisco, CA"}

Dear ${candidateName},

We are pleased to extend this formal offer of employment for the position of ${designation} (${gradeLevel}) with My Organisation. We were extremely impressed by your experience and believe you will make significant contributions to our ${departmentName} team.

1. POSITION & REPORTING
You will serve in the role of ${designation}, reporting directly to ${reportingManager}. Your primary work arrangement is designated as ${workLocation}. Your proposed start date will be ${new Date(joiningDate).toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric" })}.

2. COMPENSATION & INCENTIVES
• Base Compensation: ${formattedSalary}
${signOnBonus && parseInt(signOnBonus) > 0 ? `• Sign-On Bonus: ${curSymbol}${parseInt(signOnBonus).toLocaleString()} payable upon standard 30-day payroll cycle.\n` : ""}${annualBonus ? `• Variable Incentive / Performance Bonus: ${annualBonus}\n` : ""}${equityShares ? `• Equity Grant: ${equityShares}\n` : ""}
3. BENEFITS & STATUTORY LEAVE
${benefits}

4. CONTRACTUAL COVENANTS
• Probationary Period: ${probationPeriod}
• Notice Period: ${noticePeriod}
• Offer Expiration: This offer remains valid until ${new Date(expiresAt).toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric" })}.

${customFields.length > 0 ? `5. SPECIAL PROVISIONS & ADDENDA\n${customFields.map((f, i) => `5.${i + 1} ${f.key}: ${f.value}`).join("\n")}\n\n` : ""}We look forward to welcoming you to the team.

Sincerely,

Authorized Signatory
My Organisation / Karmarat`;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedApp) {
      toast.error("Please select an applicant");
      return;
    }
    // An offer is a number somebody has to honour. A blank field defaulting to
    // 150000 would put a figure in a letter that nobody chose and the candidate
    // would read as the agreed salary.
    if (!baseSalary.trim() || !Number.isFinite(parseInt(baseSalary))) {
      toast.error("Enter a base salary for this offer");
      return;
    }

    setIsSubmitting(true);
    try {
      const letterContent = generateLetterContent();

      await createOffer({
        applicationId: selectedApp.id,
        candidateId: selectedApp.candidateId,
        designation,
        departmentName,
        gradeLevel,
        baseSalary: parseInt(baseSalary),
        currency,
        payFrequency,
        signOnBonus: signOnBonus ? parseInt(signOnBonus) : undefined,
        annualBonus,
        equityShares,
        joiningDate,
        expiresAt: expiresAt ? new Date(expiresAt) : null,
        reportingManager,
        workLocation,
        probationPeriod,
        noticePeriod,
        benefitsSummary: benefits,
        templateType,
        customFields: customFields.filter((f) => f.key.trim() && f.value.trim()),
        offerLetterContent: letterContent,
      });

      toast.success(`Formal offer letter package generated for ${selectedApp.candidateName}!`);
      router.push("/offers");
    } catch (err: any) {
      toast.error(err.message || "Failed to create offer package");
    } finally {
      setIsSubmitting(false);
    }
  };

  const handlePrint = () => {
    window.print();
  };

  return (
    <div className="page max-w-6xl space-y-5 font-sans">
      <PageHeader
        title="Generate Offer Letter Package"
        description="Comprehensive compensation suite, dynamic contractual provisions, and live interactive letter generation."
        breadcrumbs={[
          { label: "Offers", href: "/offers" },
          { label: "Create Offer Package" },
        ]}
        actions={
          <div className="flex items-center gap-2 flex-wrap">
            <div className="flex items-center border border-border rounded-xs overflow-hidden bg-card">
              <button
                type="button"
                onClick={() => setViewMode("builder")}
                className={cn(
                  "px-3 py-1 text-xs font-medium transition-colors cursor-pointer",
                  viewMode === "builder" ? "bg-copper text-white" : "text-muted-foreground hover:text-foreground",
                )}
              >
                Package Builder
              </button>
              <button
                type="button"
                onClick={() => setViewMode("preview")}
                className={cn(
                  "px-3 py-1 text-xs font-medium transition-colors flex items-center gap-1 cursor-pointer",
                  viewMode === "preview" ? "bg-copper text-white" : "text-muted-foreground hover:text-foreground",
                )}
              >
                <Eye className="size-3" />
                <span>Live Document Preview</span>
              </button>
            </div>

            <Link href="/offers">
              <Button variant="outline" size="sm" className="gap-1 text-xs">
                <ArrowLeft className="size-3.5" />
                <span>Cancel</span>
              </Button>
            </Link>

            {viewMode === "preview" && (
              <Button size="sm" variant="outline" onClick={handlePrint} className="gap-1 text-xs">
                <Printer className="size-3.5" />
                <span>Print / PDF</span>
              </Button>
            )}

            <Button
              size="sm"
              variant="accent"
              disabled={isSubmitting || loading}
              onClick={handleSubmit}
              className="gap-1 text-xs"
            >
              {isSubmitting ? <Loader2 className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />}
              <span>Save &amp; Generate Offer</span>
            </Button>
          </div>
        }
      />

      {loading ? (
        <div className="p-16 text-center text-xs text-muted-foreground flex items-center justify-center gap-2">
          <Loader2 className="size-4 animate-spin text-copper" />
          <span>Loading applicants and enterprise taxonomies...</span>
        </div>
      ) : viewMode === "preview" ? (
        /* LIVE INTERACTIVE OFFER LETTER DOCUMENT PREVIEW */
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-5">
          {/* Main Paper Document */}
          <div className="lg:col-span-2">
            <Card className="p-8 sm:p-12 shadow-md border-border bg-white text-stone-900 font-sans space-y-6 rounded-xs">
              {/* Company Letterhead */}
              <div className="flex items-center justify-between border-b-2 border-stone-800 pb-4">
                <div>
                  <h1 className="text-xl font-bold tracking-tight text-stone-900 uppercase">
                    Karmarat
                  </h1>
                  <span className="text-xs text-stone-500 font-medium">
                    Corporate Headquarters · Talent Operations &amp; HR
                  </span>
                </div>
                <div className="text-right">
                  <Badge variant="outline" className="text-stone-700 border-stone-300 font-bold uppercase text-[10px]">
                    Formal Employment Offer
                  </Badge>
                  <div className="text-[11px] text-stone-500 mt-1">
                    Date: {new Date().toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric" })}
                  </div>
                </div>
              </div>

              {/* Recipient Box */}
              <div className="text-xs space-y-0.5 text-stone-800">
                <div className="font-semibold text-sm">{selectedApp?.candidateName || "Candidate Name"}</div>
                <div>{selectedApp?.candidateEmail || "candidate@email.com"}</div>
                <div>{selectedApp?.candidateCity || "San Francisco, CA"}</div>
              </div>

              {/* Salutation & Intro */}
              <div className="text-xs text-stone-800 leading-relaxed space-y-2">
                <p>Dear <strong>{selectedApp?.candidateName || "Candidate"}</strong>,</p>
                <p>
                  On behalf of <strong>My Organisation</strong>, we are thrilled to extend this formal offer of employment for the position of <strong>{designation}</strong> ({gradeLevel}) within our <strong>{departmentName}</strong> team.
                </p>
              </div>

              {/* Position & Work Mode */}
              <div className="p-4 rounded-xs bg-stone-50 border border-stone-200 text-xs space-y-2">
                <h3 className="font-bold uppercase text-[10px] text-stone-600 tracking-wider">
                  1. Position &amp; Reporting Structure
                </h3>
                <div className="grid grid-cols-2 gap-2 text-stone-800">
                  <div>• Role Title: <strong>{designation}</strong></div>
                  <div>• Level / Grade: <strong>{gradeLevel}</strong></div>
                  <div>• Reporting Supervisor: <strong>{reportingManager}</strong></div>
                  <div>• Primary Location: <strong>{workLocation}</strong></div>
                  <div>• Start Date: <strong>{new Date(joiningDate).toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric" })}</strong></div>
                  <div>• Offer Decision Deadline: <strong>{new Date(expiresAt).toLocaleDateString("en-US", { year: "numeric", month: "long", day: "numeric" })}</strong></div>
                </div>
              </div>

              {/* Compensation Breakdown Table */}
              <div className="p-4 rounded-xs bg-stone-50 border border-stone-200 text-xs space-y-2.5">
                <h3 className="font-bold uppercase text-[10px] text-stone-600 tracking-wider">
                  2. Compensation &amp; Equity Framework
                </h3>
                <div className="space-y-1.5 text-stone-800">
                  <div className="flex justify-between border-b border-stone-200 pb-1">
                    <span>Base Annual Salary ({currency}):</span>
                    <span className="font-bold">
                      {currency === "USD" ? "$" : currency === "EUR" ? "€" : currency === "GBP" ? "£" : currency === "INR" ? "₹" : `${currency} `}
                      {parseInt(baseSalary || "0").toLocaleString()} / {payFrequency}
                    </span>
                  </div>
                  {signOnBonus && parseInt(signOnBonus) > 0 && (
                    <div className="flex justify-between border-b border-stone-200 pb-1">
                      <span>Sign-On / Joining Bonus:</span>
                      <span className="font-bold text-emerald-700">
                        {currency === "USD" ? "$" : `${currency} `}{parseInt(signOnBonus).toLocaleString()}
                      </span>
                    </div>
                  )}
                  {annualBonus && (
                    <div className="flex justify-between border-b border-stone-200 pb-1">
                      <span>Performance / Variable Incentive:</span>
                      <span className="font-medium text-right max-w-xs">{annualBonus}</span>
                    </div>
                  )}
                  {equityShares && (
                    <div className="flex justify-between border-b border-stone-200 pb-1">
                      <span>Equity Stock Grant:</span>
                      <span className="font-medium text-right max-w-xs">{equityShares}</span>
                    </div>
                  )}
                </div>
              </div>

              {/* Benefits & Perks */}
              <div className="p-4 rounded-xs bg-stone-50 border border-stone-200 text-xs space-y-1.5 text-stone-800">
                <h3 className="font-bold uppercase text-[10px] text-stone-600 tracking-wider">
                  3. Health, Leave &amp; Statutory Benefits
                </h3>
                <p className="leading-relaxed">{benefits}</p>
                <div className="text-[11px] text-stone-600 pt-1">
                  Probation Period: <strong>{probationPeriod}</strong> • Notice Period: <strong>{noticePeriod}</strong>
                </div>
              </div>

              {/* Custom Provisions / Addenda */}
              {customFields.length > 0 && (
                <div className="p-4 rounded-xs bg-stone-50 border border-stone-200 text-xs space-y-2 text-stone-800">
                  <h3 className="font-bold uppercase text-[10px] text-stone-600 tracking-wider">
                    4. Special Provisions &amp; Covenants
                  </h3>
                  <div className="space-y-1.5">
                    {customFields.map((field, idx) => (
                      <div key={idx} className="flex items-start gap-2">
                        <span className="font-semibold shrink-0">4.{idx + 1} {field.key}:</span>
                        <span className="text-stone-700">{field.value}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* Signatures */}
              <div className="pt-6 border-t-2 border-stone-300 grid grid-cols-2 gap-8 text-xs text-stone-800">
                <div className="space-y-4">
                  <div className="border-b border-stone-400 pb-8 font-serif italic text-stone-500">
                    Authorized Officer Signature
                  </div>
                  <div>
                    <strong>My Organisation Representative</strong>
                    <div className="text-[11px] text-stone-500">Executive Talent Officer</div>
                  </div>
                </div>
                <div className="space-y-4">
                  <div className="border-b border-stone-400 pb-8 font-serif italic text-stone-500">
                    Candidate Acceptance Signature
                  </div>
                  <div>
                    <strong>{selectedApp?.candidateName || "Candidate Name"}</strong>
                    <div className="text-[11px] text-stone-500">Date: ________________________</div>
                  </div>
                </div>
              </div>
            </Card>
          </div>

          {/* Quick Config Sidebar in Preview Mode */}
          <div className="space-y-4">
            <Card className="p-4 shadow-none border-border bg-card space-y-3">
              <h3 className="text-xs font-semibold text-foreground uppercase tracking-wider flex items-center gap-1.5">
                <Sparkles className="size-3.5 text-copper" />
                <span>Package Summary</span>
              </h3>
              <div className="space-y-2 text-xs">
                <div className="flex justify-between py-1 border-b border-border/60">
                  <span className="text-muted-foreground">Candidate:</span>
                  <span className="font-medium text-foreground">{selectedApp?.candidateName}</span>
                </div>
                <div className="flex justify-between py-1 border-b border-border/60">
                  <span className="text-muted-foreground">Role:</span>
                  <span className="font-medium text-foreground">{designation}</span>
                </div>
                <div className="flex justify-between py-1 border-b border-border/60">
                  <span className="text-muted-foreground">Total Annual Base:</span>
                  <span className="font-semibold text-copper">
                    {currency} {parseInt(baseSalary || "0").toLocaleString()}
                  </span>
                </div>
                <div className="flex justify-between py-1 border-b border-border/60">
                  <span className="text-muted-foreground">Joining Date:</span>
                  <span className="font-medium text-foreground">{joiningDate}</span>
                </div>
                <div className="flex justify-between py-1 border-b border-border/60">
                  <span className="text-muted-foreground">Custom Clauses:</span>
                  <span className="font-medium text-foreground">{customFields.length} Defined</span>
                </div>
              </div>

              <div className="pt-2 space-y-2">
                <Button
                  className="w-full text-xs gap-1.5"
                  variant="accent"
                  disabled={isSubmitting}
                  onClick={handleSubmit}
                >
                  {isSubmitting ? <Loader2 className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />}
                  <span>Confirm &amp; Issue Offer</span>
                </Button>

                <Button
                  className="w-full text-xs gap-1.5"
                  variant="outline"
                  onClick={() => setViewMode("builder")}
                >
                  <span>Return to Package Builder</span>
                </Button>
              </div>
            </Card>
          </div>
        </div>
      ) : (
        /* PACKAGE BUILDER FORM VIEW */
        <form onSubmit={handleSubmit} className="space-y-5">
          {/* 1. Target Candidate & Template Selector */}
          <Card className="p-5 shadow-none border-border bg-card space-y-4">
            <div className="flex items-center justify-between border-b border-border/60 pb-3">
              <div>
                <h2 className="text-sm font-semibold text-foreground flex items-center gap-2">
                  <User className="size-4 text-copper" />
                  <span>Applicant &amp; Package Template</span>
                </h2>
                <p className="text-xs text-muted-foreground">
                  Select candidate from active pipeline and choose pre-configured executive / engineering tier.
                </p>
              </div>
              <Badge variant="outline" className="text-xs border-copper/30 text-copper font-medium">
                Step 1 of 4
              </Badge>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Target Candidate Application *</label>
                <select
                  value={selectedAppId}
                  onChange={(e) => handleAppSelect(e.target.value)}
                  className="w-full h-8 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:ring-1 focus:ring-copper cursor-pointer"
                >
                  {applications.map((app) => (
                    <option key={app.id} value={app.id}>
                      {app.candidateName} — {app.jobTitle} ({app.stage.toUpperCase()})
                    </option>
                  ))}
                </select>
                {selectedApp && (
                  <div className="text-[11px] text-muted-foreground flex items-center gap-2 pt-0.5">
                    <span>Email: <strong className="text-foreground">{selectedApp.candidateEmail}</strong></span>
                    <span>•</span>
                    <span>Current: <strong className="text-foreground">{selectedApp.currentDesignation || "Engineer"}</strong></span>
                  </div>
                )}
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Offer Package Template *</label>
                <select
                  value={templateType}
                  onChange={(e) => handleTemplateChange(e.target.value)}
                  className="w-full h-8 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:ring-1 focus:ring-copper cursor-pointer"
                >
                  {OFFER_TEMPLATES.map((tpl) => (
                    <option key={tpl.id} value={tpl.id}>
                      {tpl.name}
                    </option>
                  ))}
                </select>
                <div className="text-[11px] text-muted-foreground pt-0.5">
                  {OFFER_TEMPLATES.find((t) => t.id === templateType)?.desc}
                </div>
              </div>
            </div>
          </Card>

          {/* 2. Position & Organizational Hierarchy */}
          <Card className="p-5 shadow-none border-border bg-card space-y-4">
            <div className="flex items-center justify-between border-b border-border/60 pb-3">
              <div>
                <h2 className="text-sm font-semibold text-foreground flex items-center gap-2">
                  <Building2 className="size-4 text-copper" />
                  <span>Role &amp; Organizational Hierarchy</span>
                </h2>
                <p className="text-xs text-muted-foreground">
                  Formal corporate designation, grade band, manager reporting, and work arrangement.
                </p>
              </div>
              <Badge variant="outline" className="text-xs border-border text-muted-foreground">
                Step 2 of 4
              </Badge>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Official Designation / Title *</label>
                <Input
                  value={designation}
                  onChange={(e) => setDesignation(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. Staff Software Engineer"
                  required
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Job Level / Grade *</label>
                <Input
                  value={gradeLevel}
                  onChange={(e) => setGradeLevel(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. IC5 / Senior Staff / VP"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Department / Unit *</label>
                <Input
                  value={departmentName}
                  onChange={(e) => setDepartmentName(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. Engineering / Product"
                  required
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Reporting Manager / Title *</label>
                <Input
                  value={reportingManager}
                  onChange={(e) => setReportingManager(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. VP of Engineering"
                  required
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Primary Location &amp; Mode *</label>
                <Input
                  value={workLocation}
                  onChange={(e) => setWorkLocation(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. San Francisco HQ / Hybrid (2 Days Remote)"
                  required
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Target Start / Joining Date *</label>
                <Input
                  type="date"
                  value={joiningDate}
                  onChange={(e) => setJoiningDate(e.target.value)}
                  className="h-8 text-xs bg-card"
                  required
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Offer Decision Deadline</label>
                <Input
                  type="date"
                  value={expiresAt}
                  onChange={(e) => setExpiresAt(e.target.value)}
                  className="h-8 text-xs bg-card"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Probation Period</label>
                <Input
                  value={probationPeriod}
                  onChange={(e) => setProbationPeriod(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. 90 Days"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Notice Period</label>
                <Input
                  value={noticePeriod}
                  onChange={(e) => setNoticePeriod(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. 30 Days"
                />
              </div>
            </div>
          </Card>

          {/* 3. Comprehensive Compensation & Equity Packaging */}
          <Card className="p-5 shadow-none border-border bg-card space-y-4">
            <div className="flex items-center justify-between border-b border-border/60 pb-3">
              <div>
                <h2 className="text-sm font-semibold text-foreground flex items-center gap-2">
                  <DollarSign className="size-4 text-copper" />
                  <span>Compensation, Bonuses &amp; Equity Framework</span>
                </h2>
                <p className="text-xs text-muted-foreground">
                  Dynamic multi-currency base compensation, joining incentives, performance targets, and equity vesting.
                </p>
              </div>
              <Badge variant="outline" className="text-xs border-border text-muted-foreground">
                Step 3 of 4
              </Badge>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Currency *</label>
                <select
                  value={currency}
                  onChange={(e) => setCurrency(e.target.value)}
                  className="w-full h-8 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:ring-1 focus:ring-copper cursor-pointer"
                >
                  {currenciesList.map((c) => (
                    <option key={c.id} value={c.code}>
                      {c.code} — {c.name} ({c.symbol})
                    </option>
                  ))}
                </select>
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Pay Frequency *</label>
                <select
                  value={payFrequency}
                  onChange={(e) => setPayFrequency(e.target.value)}
                  className="w-full h-8 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:ring-1 focus:ring-copper cursor-pointer capitalize"
                >
                  {payFrequenciesList.map((f) => (
                    <option key={f.id} value={f.name.toLowerCase()}>
                      {f.name}
                    </option>
                  ))}
                </select>
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Base Salary Amount *</label>
                <Input
                  type="number"
                  value={baseSalary}
                  onChange={(e) => setBaseSalary(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. 175000"
                  required
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Sign-On / Joining Bonus (Amount)</label>
                <Input
                  type="number"
                  value={signOnBonus}
                  onChange={(e) => setSignOnBonus(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. 15000"
                />
              </div>

              <div className="sm:col-span-2 space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Performance / Variable Bonus Plan</label>
                <Input
                  value={annualBonus}
                  onChange={(e) => setAnnualBonus(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. 15% Target Performance Bonus paid annually"
                />
              </div>

              <div className="sm:col-span-3 space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Equity Stock Grant &amp; Vesting Schedule</label>
                <Input
                  value={equityShares}
                  onChange={(e) => setEquityShares(e.target.value)}
                  className="h-8 text-xs bg-card"
                  placeholder="e.g. 12,500 Incentive Stock Options (ISO), 4-Year Vesting with 1-Year Cliff"
                />
              </div>

              <div className="sm:col-span-3 space-y-1.5">
                <label className="text-xs font-semibold text-foreground">Benefits, Healthcare &amp; Perks Package</label>
                <Textarea
                  rows={2}
                  value={benefits}
                  onChange={(e) => setBenefits(e.target.value)}
                  className="text-xs bg-card"
                  placeholder="Comprehensive Medical, Dental, Vision, 401(k) Match..."
                />
              </div>
            </div>
          </Card>

          {/* 4. Dynamic Addable Custom Fields / Special Clauses Builder */}
          <Card className="p-5 shadow-none border-border bg-card space-y-4">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 border-b border-border/60 pb-3">
              <div>
                <h2 className="text-sm font-semibold text-foreground flex items-center gap-2">
                  <ShieldCheck className="size-4 text-copper" />
                  <span>Dynamic Custom Fields &amp; Special Clauses</span>
                </h2>
                <p className="text-xs text-muted-foreground">
                  Add arbitrary bespoke provisions (e.g. Visa Sponsorship, Hardware Allowance, Retention Milestone, Relocation).
                </p>
              </div>
              <Button
                type="button"
                size="xs"
                variant="outline"
                onClick={addCustomField}
                className="gap-1 text-xs border-copper/30 text-copper hover:bg-copper/10 shrink-0"
              >
                <Plus className="size-3.5" />
                <span>Add Custom Field</span>
              </Button>
            </div>

            {/* Quick Add Presets Bar */}
            <div className="flex items-center gap-1.5 flex-wrap">
              <span className="text-[11px] text-muted-foreground font-medium">Quick Clause Presets:</span>
              {PREMADE_CUSTOM_PROVISIONS.map((p, idx) => (
                <button
                  key={idx}
                  type="button"
                  onClick={() => addPremadeProvision(p)}
                  className="text-[10px] px-2 py-0.5 rounded-xs border border-border bg-muted/40 hover:bg-muted text-foreground hover:text-copper transition-colors cursor-pointer"
                >
                  + {p.key}
                </button>
              ))}
            </div>

            {/* Custom Fields Repeater */}
            <div className="space-y-2.5">
              {customFields.length === 0 ? (
                <div className="p-6 text-center border border-dashed border-border rounded-xs bg-muted/10 text-xs text-muted-foreground">
                  No special custom clauses added. Click &quot;Add Custom Field&quot; or select a preset above.
                </div>
              ) : (
                customFields.map((field, idx) => (
                  <div
                    key={idx}
                    className="flex flex-col sm:flex-row items-start sm:items-center gap-2 p-2.5 rounded-xs border border-border bg-muted/20"
                  >
                    <div className="w-full sm:w-1/3">
                      <Input
                        value={field.key}
                        onChange={(e) => updateCustomField(idx, "key", e.target.value)}
                        placeholder="Field / Clause Title (e.g. Visa Sponsorship)"
                        className="h-7 text-xs bg-card font-medium"
                      />
                    </div>
                    <div className="w-full sm:flex-1">
                      <Input
                        value={field.value}
                        onChange={(e) => updateCustomField(idx, "value", e.target.value)}
                        placeholder="Clause description or terms..."
                        className="h-7 text-xs bg-card"
                      />
                    </div>
                    <button
                      type="button"
                      onClick={() => removeCustomField(idx)}
                      className="size-7 rounded-xs border border-border hover:border-destructive hover:bg-destructive/10 text-muted-foreground hover:text-destructive flex items-center justify-center transition-colors shrink-0 cursor-pointer"
                      title="Remove field"
                    >
                      <Trash2 className="size-3.5" />
                    </button>
                  </div>
                ))
              )}
            </div>
          </Card>

          {/* Action Footer Bar */}
          <div className="flex items-center justify-between gap-3 pt-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => setViewMode("preview")}
              className="gap-1 text-xs"
            >
              <Eye className="size-3.5 text-copper" />
              <span>Preview Formatted Offer Letter</span>
            </Button>

            <div className="flex items-center gap-2">
              <Link href="/offers">
                <Button type="button" variant="outline" size="sm" className="text-xs">
                  Cancel
                </Button>
              </Link>
              <Button
                type="submit"
                size="sm"
                variant="accent"
                disabled={isSubmitting || loading}
                className="gap-1 text-xs"
              >
                {isSubmitting ? <Loader2 className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />}
                <span>Save &amp; Generate Offer</span>
              </Button>
            </div>
          </div>
        </form>
      )}
    </div>
  );
}

export function OfferCreatePage() {
  return (
    <Suspense
      fallback={
        <div className="p-16 text-center text-xs text-muted-foreground flex items-center justify-center gap-2">
          <Loader2 className="size-4 animate-spin text-copper" />
          <span>Loading Offer Package Generator...</span>
        </div>
      }
    >
      <CreateOfferContent />
    </Suspense>
  );
}
