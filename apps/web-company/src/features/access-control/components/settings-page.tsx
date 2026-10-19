"use client";

import React, { useState, useEffect, useRef, Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { cn } from "@/lib/utils";
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@/components/ui/tabs";
import { PageHeader } from "@/components/shared/page-header";
import {
  CheckCircle2,
  Plus,
  Trash2,
  Edit2,
  Loader2,
  ShieldCheck,
  MapPin,
  Lock,
  UserPlus,
  Check,
  X,
  ChevronLeft,
  ChevronRight,
  Mail,
  Server,
  KeyRound,
  Eye,
  EyeOff,
  Send,
  ShieldAlert,
  Sparkles,
  RefreshCw,
} from "lucide-react";
import {
  saveSmtpConfig,
  testSmtpConnection,
} from "@/features/organization/server/actions";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import { toast } from "sonner";
import {
  createDepartment,
  updateDepartment,
  deleteDepartment,
  createLocation,
  updateLocation,
  deleteLocation,
  createWorkMode,
  updateWorkMode,
  deleteWorkMode,
  createEmploymentType,
  updateEmploymentType,
  deleteEmploymentType,
  createExperienceLevel,
  updateExperienceLevel,
  deleteExperienceLevel,
  createEducationLevel,
  updateEducationLevel,
  deleteEducationLevel,
  createCurrency,
  updateCurrency,
  deleteCurrency,
  createPayFrequency,
  updatePayFrequency,
  deletePayFrequency,
  createJobStatus,
  updateJobStatus,
  deleteJobStatus,
  createInterviewType,
  updateInterviewType,
  deleteInterviewType,
  createBenefitCategory,
  updateBenefitCategory,
  deleteBenefitCategory,
} from "@/features/masters/server/actions";
import {
  updateOrganizationSettings,
  updateIntegrationSettings,
} from "@/features/organization/server/actions";
import {
  createUser,
  updateUserRoles,
  toggleUserActive,
  deleteUser,
} from "@/features/access-control/server/users";
import {
  TableShell,
  Table as DTable,
  THead,
  TH,
  TBody,
  TR,
  TD,
  EmptyRow,
} from "@/components/shared/data-table";
import {
  createRole,
  updateRole,
  toggleRolePermission,
  deleteRole,
  type RoleRecord,
} from "@/features/access-control/server/roles";
import {
  getFeatureAccess,
  setFeatureEnabled,
  type FeatureAccessRow,
} from "@/features/access-control/server/feature-access";
import { getSettingsBootstrap } from "@/features/access-control/server/settings-bootstrap";
import {
  getAuditLogs,
  type AuditLogRow,
} from "@/features/access-control/server/audit";
// The Feature Access tab lists this app's own modules, which is the one place
// the local registry is still the right source — the platform has no per-tenant
// feature switches and no key for a master-data screen.
import { FEATURE_GROUPS } from "@/lib/rbac/registry";
import { EMPTY_CATALOGUE, type PermissionCatalogue } from "@/lib/rbac/catalogue";
import { visibleSettingsTabs } from "@/lib/rbac/navigation";
import { Icon, hasIcon } from "@/features/shell/components/icon";
import { Can } from "@/components/rbac/can";
import { PermissionPicker } from "@/features/access-control/components/permission-picker";
import { useAccess } from "@/components/rbac/access-provider";
import { AccessDenied } from "@/components/rbac/access-denied";

function SettingsContent() {
  const searchParams = useSearchParams();
  const rawTab = searchParams.get("tab");
  const { access, isSuperAdmin, user } = useAccess();

  /**
   * Tabs are generated from the feature registry: each feature that contributes a
   * settings tab appears here automatically, gated by its own permissions. Adding
   * a feature never means editing this screen's tab list.
   */
  const availableTabs = React.useMemo(() => visibleSettingsTabs(access), [access]);
  const tabVisible = React.useCallback(
    (tab: string) => availableTabs.some((t) => t.tab === tab),
    [availableTabs],
  );

  // Legacy query aliases kept so existing bookmarks keep working.
  const TAB_ALIASES: Record<string, string> = {
    profile: "company",
    roles: "users",
    permissions: "rbac",
    currency: "currencies",
    pay_frequencies: "pay-frequencies",
    "pay-frequency": "pay-frequencies",
    job_statuses: "job-statuses",
    "requisition-statuses": "job-statuses",
    interview_types: "interview-types",
    rounds: "interview-types",
    benefit_categories: "benefit-categories",
    benefits: "benefit-categories",
    work_modes: "work-modes",
    employment_types: "employment-types",
    experience_levels: "experience-levels",
    education_levels: "education-levels",
    email: "smtp",
    mail: "smtp",
    modules: "features",
    "audit-logs": "audit",
  };

  const normalizeTab = React.useCallback(
    (tab: string | null): string => {
      const resolved = tab ? (TAB_ALIASES[tab] ?? tab) : null;
      if (resolved && availableTabs.some((t) => t.tab === resolved)) return resolved;
      return availableTabs[0]?.tab ?? "company";
    },
    [availableTabs],
  );

  const [activeTab, setActiveTab] = useState(() => normalizeTab(rawTab));

  const canViewCompany = tabVisible("company");
  const canViewRBAC = tabVisible("rbac");
  const canViewUsers = tabVisible("users");
  const canViewFeatures = tabVisible("features");
  const canViewAudit = tabVisible("audit");
  const canViewDepts = tabVisible("departments");
  const canViewLocations = tabVisible("locations");
  const canViewCurrencies = tabVisible("currencies");
  const canViewPayFrequencies = tabVisible("pay-frequencies");
  const canViewJobStatuses = tabVisible("job-statuses");
  const canViewInterviewTypes = tabVisible("interview-types");
  const canViewBenefitCategories = tabVisible("benefit-categories");
  const canViewWorkModes = tabVisible("work-modes");
  const canViewEmpTypes = tabVisible("employment-types");
  const canViewExpLevels = tabVisible("experience-levels");
  const canViewEduLevels = tabVisible("education-levels");
  const canViewSMTP = tabVisible("smtp");
  const canViewSDK = tabVisible("integrations");
  const canAssignPermissions = access.can("roles.assign_permissions");
  const canAssignUserRoles = access.can("users.assign_roles");
  const canToggleFeatures = access.can("feature-access.update");

  const currentUserId = user?.id ?? null;

  useEffect(() => {
    setActiveTab(normalizeTab(rawTab));
  }, [rawTab, normalizeTab]);

  // Data states
  const [, setOrg] = useState<any>(null);
  const [usersList, setUsersList] = useState<any[]>([]);
  const [rolesList, setRolesList] = useState<RoleRecord[]>([]);
  /**
   * The permission vocabulary the role editor speaks.
   *
   * The identity service owns roles now, and it stores its own keys. This app's
   * registry still drives navigation, route rules and the settings tabs — those
   * are this app's own structure — but a permission matrix drawn from it would
   * offer checkboxes identity cannot save and hide the ones it can.
   */
  const [catalogue, setCatalogue] = useState<PermissionCatalogue>(EMPTY_CATALOGUE);
  const [departments, setDepartments] = useState<any[]>([]);
  const [locations, setLocations] = useState<any[]>([]);
  const [currenciesList, setCurrenciesList] = useState<any[]>([]);
  const [payFrequenciesList, setPayFrequenciesList] = useState<any[]>([]);
  const [jobStatusesList, setJobStatusesList] = useState<any[]>([]);
  const [interviewTypesList, setInterviewTypesList] = useState<any[]>([]);
  const [benefitCategoriesList, setBenefitCategoriesList] = useState<any[]>([]);
  const [workModesList, setWorkModesList] = useState<any[]>([]);
  const [employmentTypesList, setEmploymentTypesList] = useState<any[]>([]);
  const [experienceLevelsList, setExperienceLevelsList] = useState<any[]>([]);
  const [educationLevelsList, setEducationLevelsList] = useState<any[]>([]);
  const [, setLoading] = useState(true);

  // Org form state
  const [orgName, setOrgName] = useState("");
  const [careersDomain, setCareersDomain] = useState("careers.myorganisation.com");
  const [timezone, setTimezone] = useState("America/Los_Angeles");
  const [defaultCurrency, setDefaultCurrency] = useState("USD");
  const [isSavingOrg, setIsSavingOrg] = useState(false);

  // Create Role Modal
  const [createRoleModalOpen, setCreateRoleModalOpen] = useState(false);
  const [roleName, setRoleName] = useState("");
  const [roleSlug, setRoleSlug] = useState("");
  const [roleDesc, setRoleDesc] = useState("");
  const [roleBadge, setRoleBadge] = useState("Custom");
  const [rolePerms, setRolePerms] = useState<Set<string>>(new Set());
  const [isCreatingRole, setIsCreatingRole] = useState(false);

  // Edit Role Modal
  const [editingRole, setEditingRole] = useState<RoleRecord | null>(null);
  const [editRoleName, setEditRoleName] = useState("");
  const [editRoleDesc, setEditRoleDesc] = useState("");
  const [editRoleBadge, setEditRoleBadge] = useState("");
  const [editRolePerms, setEditRolePerms] = useState<Set<string>>(new Set());
  const [isUpdatingRole, setIsUpdatingRole] = useState(false);

  // Matrix cell toggling state
  const [togglingKey, setTogglingKey] = useState<string | null>(null);
  const [matrixFeatureFilter, setMatrixFeatureFilter] = useState<string>("all");

  // Feature access (module enablement)
  const [featureRows, setFeatureRows] = useState<FeatureAccessRow[]>([]);
  const [togglingFeature, setTogglingFeature] = useState<string | null>(null);

  // Audit trail
  const [auditRows, setAuditRows] = useState<AuditLogRow[]>([]);
  const [auditLoading, setAuditLoading] = useState(false);
  const [auditSearch, setAuditSearch] = useState("");

  // Add User Modal
  const [userModalOpen, setUserModalOpen] = useState(false);
  const [newUserName, setNewUserName] = useState("");
  const [newUserEmail, setNewUserEmail] = useState("");
  // Empty on purpose. A pre-filled password is the one every new account keeps.
  const [newUserPassword, setNewUserPassword] = useState("");
  const [newUserRoleIds, setNewUserRoleIds] = useState<string[]>([]);

  // Assign-roles modal
  const [assigningUser, setAssigningUser] = useState<any | null>(null);
  const [assignRoleIds, setAssignRoleIds] = useState<string[]>([]);
  const [assignPrimaryRoleId, setAssignPrimaryRoleId] = useState<string>("");
  const [isAssigningRoles, setIsAssigningRoles] = useState(false);
  const [isCreatingUser, setIsCreatingUser] = useState(false);

  // Add Department Modal
  const [deptModalOpen, setDeptModalOpen] = useState(false);
  const [newDeptName, setNewDeptName] = useState("");
  const [newDeptCode, setNewDeptCode] = useState("");
  const [isCreatingDept, setIsCreatingDept] = useState(false);

  // Add Location Modal
  const [locModalOpen, setLocModalOpen] = useState(false);
  const [newLocName, setNewLocName] = useState("");
  const [newLocCity, setNewLocCity] = useState("");
  const [newLocCountry, setNewLocCountry] = useState("United States");
  const [isCreatingLoc, setIsCreatingLoc] = useState(false);

  // Add Currency Modal
  const [currencyModalOpen, setCurrencyModalOpen] = useState(false);
  const [newCurrCode, setNewCurrCode] = useState("");
  const [newCurrSymbol, setNewCurrSymbol] = useState("");
  const [newCurrName, setNewCurrName] = useState("");
  const [newCurrDefault, setNewCurrDefault] = useState(false);
  const [isCreatingCurrency, setIsCreatingCurrency] = useState(false);

  // Edit Currency Modal
  const [editingCurrency, setEditingCurrency] = useState<any>(null);
  const [editCurrCode, setEditCurrCode] = useState("");
  const [editCurrSymbol, setEditCurrSymbol] = useState("");
  const [editCurrName, setEditCurrName] = useState("");
  const [editCurrDefault, setEditCurrDefault] = useState(false);
  const [isUpdatingCurrency, setIsUpdatingCurrency] = useState(false);

  // Add Pay Frequency Modal
  const [payFreqModalOpen, setPayFreqModalOpen] = useState(false);
  const [newPayFreqName, setNewPayFreqName] = useState("");
  const [newPayFreqSlug, setNewPayFreqSlug] = useState("");
  const [newPayFreqDesc, setNewPayFreqDesc] = useState("");
  const [newPayFreqDefault, setNewPayFreqDefault] = useState(false);
  const [isCreatingPayFreq, setIsCreatingPayFreq] = useState(false);

  // Edit Pay Frequency Modal
  const [editingPayFreq, setEditingPayFreq] = useState<any>(null);
  const [editPayFreqName, setEditPayFreqName] = useState("");
  const [editPayFreqSlug, setEditPayFreqSlug] = useState("");
  const [editPayFreqDesc, setEditPayFreqDesc] = useState("");
  const [editPayFreqDefault, setEditPayFreqDefault] = useState(false);
  const [isUpdatingPayFreq, setIsUpdatingPayFreq] = useState(false);

  // Add Requisition Status Modal
  const [jobStatusModalOpen, setJobStatusModalOpen] = useState(false);
  const [newStatusName, setNewStatusName] = useState("");
  const [newStatusSlug, setNewStatusSlug] = useState("");
  const [newStatusBadge, setNewStatusBadge] = useState("secondary");
  const [newStatusDesc, setNewStatusDesc] = useState("");
  const [newStatusDefault, setNewStatusDefault] = useState(false);
  const [isCreatingJobStatus, setIsCreatingJobStatus] = useState(false);

  // Edit Requisition Status Modal
  const [editingJobStatus, setEditingJobStatus] = useState<any>(null);
  const [editStatusName, setEditStatusName] = useState("");
  const [editStatusSlug, setEditStatusSlug] = useState("");
  const [editStatusBadge, setEditStatusBadge] = useState("secondary");
  const [editStatusDesc, setEditStatusDesc] = useState("");
  const [editStatusDefault, setEditStatusDefault] = useState(false);
  const [isUpdatingJobStatus, setIsUpdatingJobStatus] = useState(false);

  // Add Interview Type Modal
  const [interviewTypeModalOpen, setInterviewTypeModalOpen] = useState(false);
  const [newITypeName, setNewITypeName] = useState("");
  const [newITypeSlug, setNewITypeSlug] = useState("");
  const [newITypeDuration, setNewITypeDuration] = useState(45);
  const [newITypeDesc, setNewITypeDesc] = useState("");
  const [newITypeDefault, setNewITypeDefault] = useState(false);
  const [isCreatingInterviewType, setIsCreatingInterviewType] = useState(false);

  // Edit Interview Type Modal
  const [editingInterviewType, setEditingInterviewType] = useState<any>(null);
  const [editITypeName, setEditITypeName] = useState("");
  const [editITypeSlug, setEditITypeSlug] = useState("");
  const [editITypeDuration, setEditITypeDuration] = useState(45);
  const [editITypeDesc, setEditITypeDesc] = useState("");
  const [editITypeDefault, setEditITypeDefault] = useState(false);
  const [isUpdatingInterviewType, setIsUpdatingInterviewType] = useState(false);

  // Add Benefit Category Modal
  const [benefitCatModalOpen, setBenefitCatModalOpen] = useState(false);
  const [newBCatName, setNewBCatName] = useState("");
  const [newBCatSlug, setNewBCatSlug] = useState("");
  const [newBCatDesc, setNewBCatDesc] = useState("");
  const [newBCatDefault, setNewBCatDefault] = useState(false);
  const [isCreatingBenefitCat, setIsCreatingBenefitCat] = useState(false);

  // Edit Benefit Category Modal
  const [editingBenefitCat, setEditingBenefitCat] = useState<any>(null);
  const [editBCatName, setEditBCatName] = useState("");
  const [editBCatSlug, setEditBCatSlug] = useState("");
  const [editBCatDesc, setEditBCatDesc] = useState("");
  const [editBCatDefault, setEditBCatDefault] = useState(false);
  const [isUpdatingBenefitCat, setIsUpdatingBenefitCat] = useState(false);

  // Add Work Mode Modal
  const [workModeModalOpen, setWorkModeModalOpen] = useState(false);
  const [newWorkModeName, setNewWorkModeName] = useState("");
  const [newWorkModeSlug, setNewWorkModeSlug] = useState("");
  const [newWorkModeDesc, setNewWorkModeDesc] = useState("");
  const [isCreatingWorkMode, setIsCreatingWorkMode] = useState(false);

  // Add Employment Type Modal
  const [empTypeModalOpen, setEmpTypeModalOpen] = useState(false);
  const [newEmpTypeName, setNewEmpTypeName] = useState("");
  const [newEmpTypeSlug, setNewEmpTypeSlug] = useState("");
  const [newEmpTypeDesc, setNewEmpTypeDesc] = useState("");
  const [isCreatingEmpType, setIsCreatingEmpType] = useState(false);

  // Add Experience Level Modal
  const [expLevelModalOpen, setExpLevelModalOpen] = useState(false);
  const [newExpLevelName, setNewExpLevelName] = useState("");
  const [newExpLevelSlug, setNewExpLevelSlug] = useState("");
  const [newExpLevelMinYears, setNewExpLevelMinYears] = useState(0);
  const [newExpLevelMaxYears, setNewExpLevelMaxYears] = useState(2);
  const [newExpLevelDesc, setNewExpLevelDesc] = useState("");
  const [isCreatingExpLevel, setIsCreatingExpLevel] = useState(false);

  // Add Education Level Modal
  const [eduLevelModalOpen, setEduLevelModalOpen] = useState(false);
  const [newEduLevelName, setNewEduLevelName] = useState("");
  const [newEduLevelSlug, setNewEduLevelSlug] = useState("");
  const [newEduLevelDesc, setNewEduLevelDesc] = useState("");
  const [isCreatingEduLevel, setIsCreatingEduLevel] = useState(false);

  // Edit Master Modals State
  const [editingDept, setEditingDept] = useState<any>(null);
  const [editDeptName, setEditDeptName] = useState("");
  const [editDeptCode, setEditDeptCode] = useState("");
  const [isUpdatingDept, setIsUpdatingDept] = useState(false);

  const [editingLoc, setEditingLoc] = useState<any>(null);
  const [editLocName, setEditLocName] = useState("");
  const [editLocCity, setEditLocCity] = useState("");
  const [editLocCountry, setEditLocCountry] = useState("United States");
  const [isUpdatingLoc, setIsUpdatingLoc] = useState(false);

  const [editingWorkMode, setEditingWorkMode] = useState<any>(null);
  const [editWorkModeName, setEditWorkModeName] = useState("");
  const [editWorkModeSlug, setEditWorkModeSlug] = useState("");
  const [editWorkModeDesc, setEditWorkModeDesc] = useState("");
  const [isUpdatingWorkMode, setIsUpdatingWorkMode] = useState(false);

  const [editingEmpType, setEditingEmpType] = useState<any>(null);
  const [editEmpTypeName, setEditEmpTypeName] = useState("");
  const [editEmpTypeSlug, setEditEmpTypeSlug] = useState("");
  const [editEmpTypeDesc, setEditEmpTypeDesc] = useState("");
  const [isUpdatingEmpType, setIsUpdatingEmpType] = useState(false);

  const [editingExpLevel, setEditingExpLevel] = useState<any>(null);
  const [editExpLevelName, setEditExpLevelName] = useState("");
  const [editExpLevelSlug, setEditExpLevelSlug] = useState("");
  const [editExpLevelMinYears, setEditExpLevelMinYears] = useState(0);
  const [editExpLevelMaxYears, setEditExpLevelMaxYears] = useState(2);
  const [editExpLevelDesc, setEditExpLevelDesc] = useState("");
  const [isUpdatingExpLevel, setIsUpdatingExpLevel] = useState(false);

  const [editingEduLevel, setEditingEduLevel] = useState<any>(null);
  const [editEduLevelName, setEditEduLevelName] = useState("");
  const [editEduLevelSlug, setEditEduLevelSlug] = useState("");
  const [editEduLevelDesc, setEditEduLevelDesc] = useState("");
  const [isUpdatingEduLevel, setIsUpdatingEduLevel] = useState(false);

  // SMTP Settings State
  const [smtpHost, setSmtpHost] = useState("smtp.resend.com");
  const [smtpPort, setSmtpPort] = useState("587");
  const [smtpSecure, setSmtpSecure] = useState(false);
  const [smtpUser, setSmtpUser] = useState("resend");
  const [smtpPass, setSmtpPass] = useState("");
  const [smtpFromName, setSmtpFromName] = useState("Karmarat Talent Team");
  const [smtpFromEmail, setSmtpFromEmail] = useState("talent@karmarat.com");
  const [smtpReplyTo, setSmtpReplyTo] = useState("recruiting@karmarat.com");
  const [smtpSignature, setSmtpSignature] = useState("--\nKarmarat Talent Acquisition\nhttps://karmarat.com");
  const [smtpAutoAppConfirm, setSmtpAutoAppConfirm] = useState(true);
  const [smtpAutoInterviewInvite, setSmtpAutoInterviewInvite] = useState(true);
  const [smtpAutoOfferNotice, setSmtpAutoOfferNotice] = useState(true);
  const [smtpConfigured, setSmtpConfigured] = useState(false);
  const [smtpLastTestedAt, setSmtpLastTestedAt] = useState<string | null>(null);
  const [smtpLastTestStatus, setSmtpLastTestStatus] = useState<"success" | "error" | null>(null);
  const [smtpLastTestMessage, setSmtpLastTestMessage] = useState<string | null>(null);

  const [smtpTestEmail, setSmtpTestEmail] = useState("");
  const [smtpTesting, setSmtpTesting] = useState(false);
  const [smtpSaving, setSmtpSaving] = useState(false);
  const [smtpLogs, setSmtpLogs] = useState<string[]>([]);
  const [showSmtpPassword, setShowSmtpPassword] = useState(false);

  // HRM / integrations
  const [hrmWebhookUrl, setHrmWebhookUrl] = useState("");
  const [isSavingIntegrations, setIsSavingIntegrations] = useState(false);

  /**
   * Loads the whole screen in a single round-trip.
   *
   * The server returns only the sections this actor may open — the same
   * permission set that decides which tabs render — so nothing arrives that the
   * UI would have to hide afterwards.
   */
  const loadAll = async () => {
    setLoading(true);
    try {
      const data = await getSettingsBootstrap();

      setOrg(data.organization);
      if (data.organization) {
        setOrgName(data.organization.name || "My Organisation");
        setCareersDomain(data.organization.careersDomain || "careers.myorganisation.com");
        setTimezone(data.organization.timezone || "America/Los_Angeles");
        setDefaultCurrency(data.organization.defaultCurrency || "USD");
      }

      setUsersList(data.users);
      setRolesList(data.roles);
      setCatalogue(data.catalogue);
      setFeatureRows(data.features);
      setDepartments(data.departments);
      setLocations(data.locations);
      setCurrenciesList(data.currencies);
      setPayFrequenciesList(data.payFrequencies);
      setJobStatusesList(data.jobStatuses);
      setInterviewTypesList(data.interviewTypes);
      setBenefitCategoriesList(data.benefitCategories);
      setWorkModesList(data.workModes);
      setEmploymentTypesList(data.employmentTypes);
      setExperienceLevelsList(data.experienceLevels);
      setEducationLevelsList(data.educationLevels);

      if (data.integrations) {
        setHrmWebhookUrl(data.integrations.hrmWebhookUrl || "");
      }

      const smtpData = data.smtp;
      if (smtpData) {
        setSmtpHost(smtpData.host || "smtp.resend.com");
        setSmtpPort(String(smtpData.port || 587));
        setSmtpSecure(Boolean(smtpData.secure));
        setSmtpUser(smtpData.user || "");
        setSmtpPass(smtpData.pass || "");
        setSmtpFromName(smtpData.fromName || "Karmarat Talent Team");
        setSmtpFromEmail(smtpData.fromEmail || "talent@karmarat.com");
        setSmtpReplyTo(smtpData.replyTo || "recruiting@karmarat.com");
        setSmtpSignature(smtpData.signature || "");
        setSmtpAutoAppConfirm(smtpData.autoSendApplicationConfirmation !== false);
        setSmtpAutoInterviewInvite(smtpData.autoSendInterviewInvite !== false);
        setSmtpAutoOfferNotice(smtpData.autoSendOfferNotice !== false);
        setSmtpConfigured(Boolean(smtpData.isConfigured));
        setSmtpLastTestedAt(smtpData.lastTestedAt || null);
        setSmtpLastTestStatus(smtpData.lastTestStatus || null);
        setSmtpLastTestMessage(smtpData.lastTestMessage || null);
        if (smtpData.fromEmail) setSmtpTestEmail(smtpData.fromEmail);
      }

      // Seeded from the roles this actor may actually hand out, not from every
      // role that exists. The dialog only renders `assignableRoles`, so a
      // default taken from the wider list was a selected id with no visible
      // checkbox — the form looked like it had nothing chosen and created the
      // member with a role nobody picked.
      const held = new Set(access.platformKeys());
      const assignable = data.roles.filter(
        (r: RoleRecord) =>
          !r.isSuperAdmin && (isSuperAdmin || r.permissions.every((key) => held.has(key))),
      );
      if (assignable[0] && newUserRoleIds.length === 0) {
        setNewUserRoleIds([assignable[0].id]);
      }
    } catch (err: any) {
      console.error(err);
      toast.error(err?.message || "Failed to load settings data");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadAll();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (activeTab === "audit") loadAudit();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab]);

  // Responsive Horizontal Tabs Scroll Controller
  const tabsScrollRef = useRef<HTMLDivElement>(null);
  const [canScrollTabsLeft, setCanScrollTabsLeft] = useState(false);
  const [canScrollTabsRight, setCanScrollTabsRight] = useState(false);

  const checkTabsScroll = () => {
    if (!tabsScrollRef.current) return;
    const { scrollLeft, scrollWidth, clientWidth } = tabsScrollRef.current;
    setCanScrollTabsLeft(scrollLeft > 4);
    setCanScrollTabsRight(scrollLeft < scrollWidth - clientWidth - 4);
  };

  useEffect(() => {
    const el = tabsScrollRef.current;
    if (!el) return;
    checkTabsScroll();
    el.addEventListener("scroll", checkTabsScroll, { passive: true });
    window.addEventListener("resize", checkTabsScroll);
    return () => {
      el.removeEventListener("scroll", checkTabsScroll);
      window.removeEventListener("resize", checkTabsScroll);
    };
  }, [rolesList, departments, locations, workModesList, employmentTypesList, experienceLevelsList, educationLevelsList]);

  const scrollTabs = (direction: "left" | "right") => {
    if (!tabsScrollRef.current) return;
    const amount = direction === "left" ? -280 : 280;
    tabsScrollRef.current.scrollBy({ left: amount, behavior: "smooth" });
  };

  /**
   * A Super Admin may delegate anything; anyone else can only hand out the
   * permissions they already hold, matching the server-side delegation guard.
   */
  const assignableRoles = React.useMemo(() => {
    if (isSuperAdmin) return rolesList;
    // Platform keys, because a role's permissions are the platform's. Comparing
    // them against this app's own keys would match nothing and quietly hide
    // every role from anyone who is not an owner.
    const held = new Set(access.platformKeys());
    return rolesList.filter(
      (r) => !r.isSuperAdmin && r.permissions.every((key) => held.has(key)),
    );
  }, [rolesList, access, isSuperAdmin]);

  /**
   * What a new role starts with: read access to the company profile, so its
   * forms can populate. Everything else is granted deliberately.
   *
   * This used to seed every `configuration.*` read from the local registry. Those
   * are master-data screens the platform has no keys for, so seeding them into a
   * platform role would have produced a role whose first permission was one the
   * service drops on save.
   */
  const roleStartingPermissions = React.useMemo(
    () =>
      catalogue.permissions
        .filter((p) => p.key === "company_profile.read")
        .map((p) => p.key),
    [catalogue],
  );

  const delegatablePermissions = React.useMemo(
    () => (isSuperAdmin ? null : new Set(access.platformKeys())),
    [access, isSuperAdmin],
  );

  /** Row counts shown next to the master-data tabs. */
  const tabCounts: Record<string, number> = {
    users: usersList.length,
    rbac: rolesList.length,
    features: featureRows.length,
    departments: departments.length,
    locations: locations.length,
    currencies: currenciesList.length,
    "pay-frequencies": payFrequenciesList.length,
    "job-statuses": jobStatusesList.length,
    "interview-types": interviewTypesList.length,
    "benefit-categories": benefitCategoriesList.length,
    "work-modes": workModesList.length,
    "employment-types": employmentTypesList.length,
    "experience-levels": experienceLevelsList.length,
    "education-levels": educationLevelsList.length,
  };

  /**
   * The active tab is client state; the query string only exists so a tab can be
   * linked to. Rewriting the URL through the History API instead of the router
   * avoids a server round-trip — and the re-mount that would discard everything
   * this screen has loaded.
   */
  const handleTabChange = (value: string) => {
    if (value === activeTab) return;
    setActiveTab(value);
    const targetUrl = value === "company" ? "/settings" : `/settings?tab=${value}`;
    window.history.replaceState(null, "", targetUrl);
  };

  const handleSaveOrg = async () => {
    setIsSavingOrg(true);
    try {
      await updateOrganizationSettings({
        name: orgName,
        careersDomain,
        timezone,
        defaultCurrency,
      });
      toast.success("Organization details saved!");
      await loadAll();
    } catch {
      toast.error("Failed to update organization");
    } finally {
      setIsSavingOrg(false);
    }
  };

  // ---------------------------------------------------------------------------
  // ROLE CRUD HANDLERS
  // ---------------------------------------------------------------------------
  const handleOpenCreateRole = () => {
    setRoleName("");
    setRoleSlug("");
    setRoleDesc("");
    setRoleBadge("Custom");
    // A new role starts with read access to configuration lookups so its forms
    // can populate; everything else is granted deliberately.
    setRolePerms(new Set(roleStartingPermissions));
    setCreateRoleModalOpen(true);
  };

  const handleCreateRole = async () => {
    if (!roleName.trim()) {
      toast.error("Please enter a role title");
      return;
    }
    setIsCreatingRole(true);
    try {
      await createRole({
        name: roleName,
        slug: roleSlug || undefined,
        description: roleDesc,
        badge: roleBadge,
        permissions: Array.from(rolePerms),
      });
      toast.success(`Custom role '${roleName}' created with ${rolePerms.size} permissions!`);
      setCreateRoleModalOpen(false);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to create custom role");
    } finally {
      setIsCreatingRole(false);
    }
  };

  const handleOpenEditRole = (r: RoleRecord) => {
    if (r.isSuperAdmin) {
      toast.info("Super Admin holds unrestricted access by definition and cannot be edited.");
      return;
    }
    setEditingRole(r);
    setEditRoleName(r.name);
    setEditRoleDesc(r.description || "");
    setEditRoleBadge(r.badge || "Custom");
    setEditRolePerms(new Set(r.permissions));
  };

  const handleSaveEditRole = async () => {
    if (!editingRole) return;
    setIsUpdatingRole(true);
    try {
      await updateRole(editingRole.id, {
        name: editRoleName,
        description: editRoleDesc,
        badge: editRoleBadge,
        permissions: Array.from(editRolePerms),
      });
      toast.success(`Role '${editRoleName}' updated successfully!`);
      setEditingRole(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update role");
    } finally {
      setIsUpdatingRole(false);
    }
  };

  const handleDeleteRole = async (r: RoleRecord) => {
    if (r.isSuperAdmin || r.isSystem) {
      toast.error("Super Admin is the single system-level role and cannot be deleted.");
      return;
    }
    if (!confirm(`Are you sure you want to permanently delete custom role '${r.name}'?`)) return;
    try {
      await deleteRole(r.id);
      toast.success(`Role '${r.name}' deleted.`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete role");
    }
  };

  // 1-Click Matrix Toggle
  const handleMatrixToggle = async (role: RoleRecord, permKey: string) => {
    if (role.isSuperAdmin) {
      toast.info("Super Admin holds every permission permanently (read-only).");
      return;
    }

    const isGranted = (role.permissions as string[]).includes(permKey);
    const keyId = `${role.id}_${permKey}`;
    setTogglingKey(keyId);

    // Optimistic UI update
    setRolesList((prev) =>
      prev.map((r) => {
        if (r.id !== role.id) return r;
        const updated = isGranted
          ? r.permissions.filter((p) => p !== permKey)
          : [...r.permissions, permKey];
        return { ...r, permissions: updated };
      }),
    );

    try {
      await toggleRolePermission(role.id, permKey, !isGranted);
      toast.success(
        !isGranted
          ? `Granted '${permKey}' to ${role.name}`
          : `Revoked '${permKey}' from ${role.name}`,
      );
    } catch (err: any) {
      toast.error(err.message || "Failed to toggle permission");
      await loadAll();
    } finally {
      setTogglingKey(null);
    }
  };

  // ---------------------------------------------------------------------------
  // USER CRUD HANDLERS
  // ---------------------------------------------------------------------------
  const handleOpenAssignRoles = (u: any) => {
    setAssigningUser(u);
    setAssignRoleIds(u.roleIds ?? []);
    setAssignPrimaryRoleId(u.primaryRoleId ?? u.roleIds?.[0] ?? "");
  };

  const handleSaveAssignedRoles = async () => {
    if (!assigningUser) return;
    if (assignRoleIds.length === 0) {
      toast.error("Assign at least one role.");
      return;
    }
    setIsAssigningRoles(true);
    try {
      await updateUserRoles(
        assigningUser.id,
        assignRoleIds,
        assignPrimaryRoleId || assignRoleIds[0],
      );
      toast.success(`Roles updated for ${assigningUser.name}.`);
      setAssigningUser(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update roles");
    } finally {
      setIsAssigningRoles(false);
    }
  };

  const handleToggleFeature = async (feature: FeatureAccessRow) => {
    setTogglingFeature(feature.key);
    try {
      await setFeatureEnabled(feature.key, !feature.isEnabled);
      toast.success(
        feature.isEnabled
          ? `${feature.name} disabled for this organization`
          : `${feature.name} enabled`,
      );
      const rows = await getFeatureAccess();
      setFeatureRows(rows);
    } catch (err: any) {
      toast.error(err.message || "Failed to update feature access");
    } finally {
      setTogglingFeature(null);
    }
  };

  const handleSaveIntegrations = async () => {
    setIsSavingIntegrations(true);
    try {
      await updateIntegrationSettings({ hrmWebhookUrl: hrmWebhookUrl.trim() });
      toast.success("Integration settings saved.");
    } catch (err: any) {
      toast.error(err.message || "Failed to save integration settings");
    } finally {
      setIsSavingIntegrations(false);
    }
  };

  const loadAudit = async () => {
    if (!canViewAudit) return;
    setAuditLoading(true);
    try {
      const rows = await getAuditLogs({ search: auditSearch || undefined, limit: 150 });
      setAuditRows(rows);
    } catch (err: any) {
      toast.error(err.message || "Failed to load audit trail");
    } finally {
      setAuditLoading(false);
    }
  };

  const handleToggleUserActive = async (userId: string, currentStatus: boolean) => {
    try {
      await toggleUserActive(userId, !currentStatus);
      toast.success(currentStatus ? "User deactivated" : "User activated");
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to toggle status");
    }
  };

  const handleDeleteUser = async (userId: string, name: string) => {
    if (!confirm(`Delete user account "${name}"?`)) return;
    try {
      await deleteUser(userId);
      toast.success(`User ${name} deleted`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete user");
    }
  };

  const handleCreateUser = async () => {
    if (!newUserName || !newUserEmail || !newUserPassword) {
      toast.error("Please fill in name, email, and password");
      return;
    }
    if (newUserRoleIds.length === 0) {
      toast.error("Assign at least one role to the new user");
      return;
    }
    setIsCreatingUser(true);
    try {
      await createUser({
        name: newUserName,
        email: newUserEmail,
        password: newUserPassword,
        roleIds: newUserRoleIds,
        primaryRoleId: newUserRoleIds[0],
      });
      toast.success(`User account for ${newUserName} created!`);
      setUserModalOpen(false);
      setNewUserName("");
      setNewUserEmail("");
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to create user");
    } finally {
      setIsCreatingUser(false);
    }
  };

  // ---------------------------------------------------------------------------
  // DEPARTMENT & LOCATION HANDLERS
  // ---------------------------------------------------------------------------
  const handleCreateDept = async () => {
    if (!newDeptName || !newDeptCode) {
      toast.error("Please fill in department name and code");
      return;
    }
    setIsCreatingDept(true);
    try {
      await createDepartment({
        name: newDeptName,
        code: newDeptCode.toUpperCase(),
      });
      toast.success(`Department "${newDeptName}" added`);
      setDeptModalOpen(false);
      setNewDeptName("");
      setNewDeptCode("");
      await loadAll();
    } catch {
      toast.error("Failed to add department");
    } finally {
      setIsCreatingDept(false);
    }
  };

  const handleDeleteDept = async (id: string, name: string) => {
    if (!confirm(`Delete department "${name}"?`)) return;
    try {
      await deleteDepartment(id);
      toast.success("Department removed");
      await loadAll();
    } catch {
      toast.error("Failed to delete department");
    }
  };

  const handleCreateLoc = async () => {
    if (!newLocName || !newLocCity) {
      toast.error("Please fill in location title and city");
      return;
    }
    setIsCreatingLoc(true);
    try {
      await createLocation({
        name: newLocName,
        city: newLocCity,
        country: newLocCountry,
      });
      toast.success(`Location "${newLocName}" added`);
      setLocModalOpen(false);
      setNewLocName("");
      setNewLocCity("");
      await loadAll();
    } catch {
      toast.error("Failed to add location");
    } finally {
      setIsCreatingLoc(false);
    }
  };

  const handleDeleteLoc = async (id: string, name: string) => {
    if (!confirm(`Delete location "${name}"?`)) return;
    try {
      await deleteLocation(id);
      toast.success("Location removed");
      await loadAll();
    } catch {
      toast.error("Failed to delete location");
    }
  };

  const handleCreateWorkMode = async () => {
    if (!newWorkModeName.trim()) {
      toast.error("Please enter a work mode name");
      return;
    }
    setIsCreatingWorkMode(true);
    try {
      await createWorkMode({
        name: newWorkModeName.trim(),
        slug: newWorkModeSlug.trim() || undefined,
        description: newWorkModeDesc.trim() || undefined,
      });
      toast.success(`Work mode "${newWorkModeName}" created`);
      setWorkModeModalOpen(false);
      setNewWorkModeName("");
      setNewWorkModeSlug("");
      setNewWorkModeDesc("");
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add work mode");
    } finally {
      setIsCreatingWorkMode(false);
    }
  };

  const handleDeleteWorkMode = async (id: string, name: string) => {
    if (!confirm(`Delete work mode "${name}"?`)) return;
    try {
      await deleteWorkMode(id);
      toast.success("Work mode removed");
      await loadAll();
    } catch {
      toast.error("Failed to delete work mode");
    }
  };

  const handleCreateEmpType = async () => {
    if (!newEmpTypeName.trim()) {
      toast.error("Please enter an employment type name");
      return;
    }
    setIsCreatingEmpType(true);
    try {
      await createEmploymentType({
        name: newEmpTypeName.trim(),
        slug: newEmpTypeSlug.trim() || undefined,
        description: newEmpTypeDesc.trim() || undefined,
      });
      toast.success(`Employment type "${newEmpTypeName}" created`);
      setEmpTypeModalOpen(false);
      setNewEmpTypeName("");
      setNewEmpTypeSlug("");
      setNewEmpTypeDesc("");
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add employment type");
    } finally {
      setIsCreatingEmpType(false);
    }
  };

  const handleDeleteEmpType = async (id: string, name: string) => {
    if (!confirm(`Delete employment type "${name}"?`)) return;
    try {
      await deleteEmploymentType(id);
      toast.success("Employment type removed");
      await loadAll();
    } catch {
      toast.error("Failed to delete employment type");
    }
  };

  // Edit Department Handlers
  const handleOpenEditDept = (dept: any) => {
    setEditingDept(dept);
    setEditDeptName(dept.name);
    setEditDeptCode(dept.code);
  };

  const handleSaveEditDept = async () => {
    if (!editingDept || !editDeptName.trim() || !editDeptCode.trim()) {
      toast.error("Please fill in department name and code");
      return;
    }
    setIsUpdatingDept(true);
    try {
      await updateDepartment(editingDept.id, {
        name: editDeptName.trim(),
        code: editDeptCode.trim().toUpperCase(),
      });
      toast.success("Department updated successfully");
      setEditingDept(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update department");
    } finally {
      setIsUpdatingDept(false);
    }
  };

  // Edit Location Handlers
  const handleOpenEditLoc = (loc: any) => {
    setEditingLoc(loc);
    setEditLocName(loc.name);
    setEditLocCity(loc.city);
    setEditLocCountry(loc.country || "United States");
  };

  const handleSaveEditLoc = async () => {
    if (!editingLoc || !editLocName.trim() || !editLocCity.trim()) {
      toast.error("Please fill in location name and city");
      return;
    }
    setIsUpdatingLoc(true);
    try {
      await updateLocation(editingLoc.id, {
        name: editLocName.trim(),
        city: editLocCity.trim(),
        country: editLocCountry.trim(),
      });
      toast.success("Location updated successfully");
      setEditingLoc(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update location");
    } finally {
      setIsUpdatingLoc(false);
    }
  };

  // Edit Work Mode Handlers
  const handleOpenEditWorkMode = (wm: any) => {
    setEditingWorkMode(wm);
    setEditWorkModeName(wm.name);
    setEditWorkModeSlug(wm.slug);
    setEditWorkModeDesc(wm.description || "");
  };

  const handleSaveEditWorkMode = async () => {
    if (!editingWorkMode || !editWorkModeName.trim()) {
      toast.error("Please enter a work mode name");
      return;
    }
    setIsUpdatingWorkMode(true);
    try {
      await updateWorkMode(editingWorkMode.id, {
        name: editWorkModeName.trim(),
        slug: editWorkModeSlug.trim() || undefined,
        description: editWorkModeDesc.trim() || undefined,
      });
      toast.success("Work mode updated successfully");
      setEditingWorkMode(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update work mode");
    } finally {
      setIsUpdatingWorkMode(false);
    }
  };

  // Edit Employment Type Handlers
  const handleOpenEditEmpType = (et: any) => {
    setEditingEmpType(et);
    setEditEmpTypeName(et.name);
    setEditEmpTypeSlug(et.slug);
    setEditEmpTypeDesc(et.description || "");
  };

  const handleSaveEditEmpType = async () => {
    if (!editingEmpType || !editEmpTypeName.trim()) {
      toast.error("Please enter an employment type name");
      return;
    }
    setIsUpdatingEmpType(true);
    try {
      await updateEmploymentType(editingEmpType.id, {
        name: editEmpTypeName.trim(),
        slug: editEmpTypeSlug.trim() || undefined,
        description: editEmpTypeDesc.trim() || undefined,
      });
      toast.success("Employment type updated successfully");
      setEditingEmpType(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update employment type");
    } finally {
      setIsUpdatingEmpType(false);
    }
  };

  // Create Experience Level Handler
  const handleCreateExpLevel = async () => {
    if (!newExpLevelName.trim()) {
      toast.error("Please enter an experience level name");
      return;
    }
    setIsCreatingExpLevel(true);
    try {
      await createExperienceLevel({
        name: newExpLevelName.trim(),
        slug: newExpLevelSlug.trim() || undefined,
        minYears: Number(newExpLevelMinYears) || 0,
        maxYears: Number(newExpLevelMaxYears) || 0,
        description: newExpLevelDesc.trim() || undefined,
      });
      toast.success(`Experience level "${newExpLevelName}" added`);
      setExpLevelModalOpen(false);
      setNewExpLevelName("");
      setNewExpLevelSlug("");
      setNewExpLevelMinYears(0);
      setNewExpLevelMaxYears(2);
      setNewExpLevelDesc("");
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add experience level");
    } finally {
      setIsCreatingExpLevel(false);
    }
  };

  const handleOpenEditExpLevel = (exp: any) => {
    setEditingExpLevel(exp);
    setEditExpLevelName(exp.name);
    setEditExpLevelSlug(exp.slug);
    setEditExpLevelMinYears(exp.minYears ?? 0);
    setEditExpLevelMaxYears(exp.maxYears ?? 0);
    setEditExpLevelDesc(exp.description || "");
  };

  const handleSaveEditExpLevel = async () => {
    if (!editingExpLevel || !editExpLevelName.trim()) {
      toast.error("Please enter an experience level name");
      return;
    }
    setIsUpdatingExpLevel(true);
    try {
      await updateExperienceLevel(editingExpLevel.id, {
        name: editExpLevelName.trim(),
        slug: editExpLevelSlug.trim() || undefined,
        minYears: Number(editExpLevelMinYears),
        maxYears: Number(editExpLevelMaxYears),
        description: editExpLevelDesc.trim() || undefined,
      });
      toast.success("Experience level updated successfully");
      setEditingExpLevel(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update experience level");
    } finally {
      setIsUpdatingExpLevel(false);
    }
  };

  const handleDeleteExpLevel = async (id: string, name: string) => {
    if (!confirm(`Permanently remove experience level "${name}"?`)) return;
    try {
      await deleteExperienceLevel(id);
      toast.success(`Removed experience level: ${name}`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete experience level");
    }
  };

  // Create Education Level Handler
  const handleCreateEduLevel = async () => {
    if (!newEduLevelName.trim()) {
      toast.error("Please enter an education level name");
      return;
    }
    setIsCreatingEduLevel(true);
    try {
      await createEducationLevel({
        name: newEduLevelName.trim(),
        slug: newEduLevelSlug.trim() || undefined,
        description: newEduLevelDesc.trim() || undefined,
      });
      toast.success(`Education requirement "${newEduLevelName}" added`);
      setEduLevelModalOpen(false);
      setNewEduLevelName("");
      setNewEduLevelSlug("");
      setNewEduLevelDesc("");
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add education requirement");
    } finally {
      setIsCreatingEduLevel(false);
    }
  };

  const handleOpenEditEduLevel = (edu: any) => {
    setEditingEduLevel(edu);
    setEditEduLevelName(edu.name);
    setEditEduLevelSlug(edu.slug);
    setEditEduLevelDesc(edu.description || "");
  };

  const handleSaveEditEduLevel = async () => {
    if (!editingEduLevel || !editEduLevelName.trim()) {
      toast.error("Please enter an education requirement name");
      return;
    }
    setIsUpdatingEduLevel(true);
    try {
      await updateEducationLevel(editingEduLevel.id, {
        name: editEduLevelName.trim(),
        slug: editEduLevelSlug.trim() || undefined,
        description: editEduLevelDesc.trim() || undefined,
      });
      toast.success("Education requirement updated successfully");
      setEditingEduLevel(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update education requirement");
    } finally {
      setIsUpdatingEduLevel(false);
    }
  };

  const handleDeleteEduLevel = async (id: string, name: string) => {
    if (!confirm(`Permanently remove education requirement "${name}"?`)) return;
    try {
      await deleteEducationLevel(id);
      toast.success(`Removed education requirement: ${name}`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete education requirement");
    }
  };

  // ---------------------------------------------------------------------------
  // CURRENCIES CRUD HANDLERS
  // ---------------------------------------------------------------------------
  const handleCreateCurrency = async () => {
    if (!newCurrCode.trim() || !newCurrSymbol.trim() || !newCurrName.trim()) {
      toast.error("Please provide currency code, symbol, and name");
      return;
    }
    setIsCreatingCurrency(true);
    try {
      await createCurrency({
        code: newCurrCode.trim().toUpperCase(),
        symbol: newCurrSymbol.trim(),
        name: newCurrName.trim(),
        isDefault: newCurrDefault,
      });
      toast.success(`Currency "${newCurrCode.toUpperCase()}" added`);
      setCurrencyModalOpen(false);
      setNewCurrCode("");
      setNewCurrSymbol("");
      setNewCurrName("");
      setNewCurrDefault(false);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add currency");
    } finally {
      setIsCreatingCurrency(false);
    }
  };

  const handleOpenEditCurrency = (curr: any) => {
    setEditingCurrency(curr);
    setEditCurrCode(curr.code);
    setEditCurrSymbol(curr.symbol);
    setEditCurrName(curr.name);
    setEditCurrDefault(curr.isDefault || false);
  };

  const handleSaveEditCurrency = async () => {
    if (!editingCurrency || !editCurrCode.trim() || !editCurrSymbol.trim() || !editCurrName.trim()) {
      toast.error("Please provide currency code, symbol, and name");
      return;
    }
    setIsUpdatingCurrency(true);
    try {
      await updateCurrency(editingCurrency.id, {
        code: editCurrCode.trim().toUpperCase(),
        symbol: editCurrSymbol.trim(),
        name: editCurrName.trim(),
        isDefault: editCurrDefault,
      });
      toast.success("Currency updated successfully");
      setEditingCurrency(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update currency");
    } finally {
      setIsUpdatingCurrency(false);
    }
  };

  const handleDeleteCurrency = async (id: string, code: string) => {
    if (!confirm(`Permanently remove currency "${code}"?`)) return;
    try {
      await deleteCurrency(id);
      toast.success(`Removed currency: ${code}`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete currency");
    }
  };

  // ---------------------------------------------------------------------------
  // PAY FREQUENCIES CRUD HANDLERS
  // ---------------------------------------------------------------------------
  const handleCreatePayFreq = async () => {
    if (!newPayFreqName.trim()) {
      toast.error("Please enter a pay frequency name");
      return;
    }
    setIsCreatingPayFreq(true);
    try {
      await createPayFrequency({
        name: newPayFreqName.trim(),
        slug: newPayFreqSlug.trim() || undefined,
        description: newPayFreqDesc.trim() || undefined,
        isDefault: newPayFreqDefault,
      });
      toast.success(`Pay frequency "${newPayFreqName}" added`);
      setPayFreqModalOpen(false);
      setNewPayFreqName("");
      setNewPayFreqSlug("");
      setNewPayFreqDesc("");
      setNewPayFreqDefault(false);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add pay frequency");
    } finally {
      setIsCreatingPayFreq(false);
    }
  };

  const handleOpenEditPayFreq = (freq: any) => {
    setEditingPayFreq(freq);
    setEditPayFreqName(freq.name);
    setEditPayFreqSlug(freq.slug);
    setEditPayFreqDesc(freq.description || "");
    setEditPayFreqDefault(freq.isDefault || false);
  };

  const handleSaveEditPayFreq = async () => {
    if (!editingPayFreq || !editPayFreqName.trim()) {
      toast.error("Please enter a pay frequency name");
      return;
    }
    setIsUpdatingPayFreq(true);
    try {
      await updatePayFrequency(editingPayFreq.id, {
        name: editPayFreqName.trim(),
        slug: editPayFreqSlug.trim() || undefined,
        description: editPayFreqDesc.trim() || undefined,
        isDefault: editPayFreqDefault,
      });
      toast.success("Pay frequency updated successfully");
      setEditingPayFreq(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update pay frequency");
    } finally {
      setIsUpdatingPayFreq(false);
    }
  };

  const handleDeletePayFreq = async (id: string, name: string) => {
    if (!confirm(`Permanently remove pay frequency "${name}"?`)) return;
    try {
      await deletePayFrequency(id);
      toast.success(`Removed pay frequency: ${name}`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete pay frequency");
    }
  };

  // ---------------------------------------------------------------------------
  // JOB STATUSES CRUD HANDLERS
  // ---------------------------------------------------------------------------
  const handleCreateJobStatus = async () => {
    if (!newStatusName.trim()) {
      toast.error("Please enter a requisition status name");
      return;
    }
    setIsCreatingJobStatus(true);
    try {
      await createJobStatus({
        name: newStatusName.trim(),
        slug: newStatusSlug.trim() || undefined,
        badgeVariant: newStatusBadge,
        description: newStatusDesc.trim() || undefined,
        isDefault: newStatusDefault,
      });
      toast.success(`Requisition status "${newStatusName}" added`);
      setJobStatusModalOpen(false);
      setNewStatusName("");
      setNewStatusSlug("");
      setNewStatusDesc("");
      setNewStatusDefault(false);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add requisition status");
    } finally {
      setIsCreatingJobStatus(false);
    }
  };

  const handleOpenEditJobStatus = (status: any) => {
    setEditingJobStatus(status);
    setEditStatusName(status.name);
    setEditStatusSlug(status.slug);
    setEditStatusBadge(status.badgeVariant || "secondary");
    setEditStatusDesc(status.description || "");
    setEditStatusDefault(status.isDefault || false);
  };

  const handleSaveEditJobStatus = async () => {
    if (!editingJobStatus || !editStatusName.trim()) {
      toast.error("Please enter a status name");
      return;
    }
    setIsUpdatingJobStatus(true);
    try {
      await updateJobStatus(editingJobStatus.id, {
        name: editStatusName.trim(),
        slug: editStatusSlug.trim() || undefined,
        badgeVariant: editStatusBadge,
        description: editStatusDesc.trim() || undefined,
        isDefault: editStatusDefault,
      });
      toast.success("Requisition status updated successfully");
      setEditingJobStatus(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update requisition status");
    } finally {
      setIsUpdatingJobStatus(false);
    }
  };

  const handleDeleteJobStatus = async (id: string, name: string) => {
    if (!confirm(`Permanently remove requisition status "${name}"?`)) return;
    try {
      await deleteJobStatus(id);
      toast.success(`Removed requisition status: ${name}`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete requisition status");
    }
  };

  // ---------------------------------------------------------------------------
  // INTERVIEW ROUND TYPES CRUD HANDLERS
  // ---------------------------------------------------------------------------
  const handleCreateInterviewType = async () => {
    if (!newITypeName.trim()) {
      toast.error("Please enter an interview round type name");
      return;
    }
    setIsCreatingInterviewType(true);
    try {
      await createInterviewType({
        name: newITypeName.trim(),
        slug: newITypeSlug.trim() || undefined,
        defaultDurationMinutes: Number(newITypeDuration) || 45,
        description: newITypeDesc.trim() || undefined,
        isDefault: newITypeDefault,
      });
      toast.success(`Interview type "${newITypeName}" added`);
      setInterviewTypeModalOpen(false);
      setNewITypeName("");
      setNewITypeSlug("");
      setNewITypeDuration(45);
      setNewITypeDesc("");
      setNewITypeDefault(false);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add interview type");
    } finally {
      setIsCreatingInterviewType(false);
    }
  };

  const handleOpenEditInterviewType = (itype: any) => {
    setEditingInterviewType(itype);
    setEditITypeName(itype.name);
    setEditITypeSlug(itype.slug);
    setEditITypeDuration(itype.defaultDurationMinutes || 45);
    setEditITypeDesc(itype.description || "");
    setEditITypeDefault(itype.isDefault || false);
  };

  const handleSaveEditInterviewType = async () => {
    if (!editingInterviewType || !editITypeName.trim()) {
      toast.error("Please enter an interview type name");
      return;
    }
    setIsUpdatingInterviewType(true);
    try {
      await updateInterviewType(editingInterviewType.id, {
        name: editITypeName.trim(),
        slug: editITypeSlug.trim() || undefined,
        defaultDurationMinutes: Number(editITypeDuration) || 45,
        description: editITypeDesc.trim() || undefined,
        isDefault: editITypeDefault,
      });
      toast.success("Interview round type updated successfully");
      setEditingInterviewType(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update interview type");
    } finally {
      setIsUpdatingInterviewType(false);
    }
  };

  const handleDeleteInterviewType = async (id: string, name: string) => {
    if (!confirm(`Permanently remove interview round type "${name}"?`)) return;
    try {
      await deleteInterviewType(id);
      toast.success(`Removed interview round type: ${name}`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete interview round type");
    }
  };

  // ---------------------------------------------------------------------------
  // BENEFIT CATEGORIES CRUD HANDLERS
  // ---------------------------------------------------------------------------
  const handleCreateBenefitCat = async () => {
    if (!newBCatName.trim()) {
      toast.error("Please enter a benefit category name");
      return;
    }
    setIsCreatingBenefitCat(true);
    try {
      await createBenefitCategory({
        name: newBCatName.trim(),
        slug: newBCatSlug.trim() || undefined,
        description: newBCatDesc.trim() || undefined,
        isDefault: newBCatDefault,
      });
      toast.success(`Benefit category "${newBCatName}" added`);
      setBenefitCatModalOpen(false);
      setNewBCatName("");
      setNewBCatSlug("");
      setNewBCatDesc("");
      setNewBCatDefault(false);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to add benefit category");
    } finally {
      setIsCreatingBenefitCat(false);
    }
  };

  const handleOpenEditBenefitCat = (bcat: any) => {
    setEditingBenefitCat(bcat);
    setEditBCatName(bcat.name);
    setEditBCatSlug(bcat.slug);
    setEditBCatDesc(bcat.description || "");
    setEditBCatDefault(bcat.isDefault || false);
  };

  const handleSaveEditBenefitCat = async () => {
    if (!editingBenefitCat || !editBCatName.trim()) {
      toast.error("Please enter a benefit category name");
      return;
    }
    setIsUpdatingBenefitCat(true);
    try {
      await updateBenefitCategory(editingBenefitCat.id, {
        name: editBCatName.trim(),
        slug: editBCatSlug.trim() || undefined,
        description: editBCatDesc.trim() || undefined,
        isDefault: editBCatDefault,
      });
      toast.success("Benefit category updated successfully");
      setEditingBenefitCat(null);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to update benefit category");
    } finally {
      setIsUpdatingBenefitCat(false);
    }
  };

  const handleDeleteBenefitCat = async (id: string, name: string) => {
    if (!confirm(`Permanently remove benefit category "${name}"?`)) return;
    try {
      await deleteBenefitCategory(id);
      toast.success(`Removed benefit category: ${name}`);
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to delete benefit category");
    }
  };

  // ---------------------------------------------------------------------------
  // SMTP CONFIGURATION HANDLERS
  // ---------------------------------------------------------------------------
  const handleSelectProviderPreset = (preset: string) => {
    switch (preset) {
      case "resend":
        setSmtpHost("smtp.resend.com");
        setSmtpPort("587");
        setSmtpSecure(false);
        setSmtpUser("resend");
        toast.success("Resend SMTP preset applied (Port 587 / STARTTLS)");
        break;
      case "sendgrid":
        setSmtpHost("smtp.sendgrid.net");
        setSmtpPort("587");
        setSmtpSecure(false);
        setSmtpUser("apikey");
        toast.success("SendGrid SMTP preset applied (Username: apikey)");
        break;
      case "ses":
        setSmtpHost("email-smtp.us-east-1.amazonaws.com");
        setSmtpPort("587");
        setSmtpSecure(false);
        toast.success("Amazon SES preset applied (Port 587 / STARTTLS)");
        break;
      case "gmail":
        setSmtpHost("smtp.gmail.com");
        setSmtpPort("465");
        setSmtpSecure(true);
        toast.success("Google Workspace preset applied (Port 465 / SSL with App Password)");
        break;
      case "mailgun":
        setSmtpHost("smtp.mailgun.org");
        setSmtpPort("587");
        setSmtpSecure(false);
        toast.success("Mailgun preset applied");
        break;
      case "postmark":
        setSmtpHost("smtp.postmarkapp.com");
        setSmtpPort("587");
        setSmtpSecure(false);
        toast.success("Postmark preset applied");
        break;
      default:
        break;
    }
  };

  const handleSaveSmtp = async () => {
    if (!smtpHost.trim() || !smtpPort.trim() || !smtpFromEmail.trim()) {
      toast.error("SMTP Host, Port, and Sender Email are required");
      return;
    }

    setSmtpSaving(true);
    try {
      await saveSmtpConfig({
        host: smtpHost.trim(),
        port: Number(smtpPort),
        secure: smtpSecure,
        user: smtpUser.trim(),
        pass: smtpPass,
        fromName: smtpFromName.trim(),
        fromEmail: smtpFromEmail.trim(),
        replyTo: smtpReplyTo.trim() || undefined,
        signature: smtpSignature,
        autoSendApplicationConfirmation: smtpAutoAppConfirm,
        autoSendInterviewInvite: smtpAutoInterviewInvite,
        autoSendOfferNotice: smtpAutoOfferNotice,
      });
      setSmtpConfigured(true);
      toast.success("SMTP email delivery settings saved successfully!");
      await loadAll();
    } catch (err: any) {
      toast.error(err.message || "Failed to save SMTP settings");
    } finally {
      setSmtpSaving(false);
    }
  };

  const handleTestSmtp = async () => {
    if (!smtpHost.trim() || !smtpPort.trim() || !smtpFromEmail.trim()) {
      toast.error("Please fill in SMTP Host, Port, and Sender Email before testing");
      return;
    }
    const targetEmail = smtpTestEmail.trim() || smtpFromEmail.trim();
    if (!targetEmail) {
      toast.error("Please provide a test recipient email address");
      return;
    }

    setSmtpTesting(true);
    setSmtpLogs(["Connecting to SMTP relay server..."]);
    try {
      const res = await testSmtpConnection(targetEmail, {
        host: smtpHost.trim(),
        port: Number(smtpPort),
        secure: smtpSecure,
        user: smtpUser.trim(),
        pass: smtpPass,
        fromName: smtpFromName.trim(),
        fromEmail: smtpFromEmail.trim(),
        replyTo: smtpReplyTo.trim() || undefined,
        signature: smtpSignature,
      });

      if (res.logs) {
        setSmtpLogs(res.logs);
      }

      if (res.success) {
        setSmtpLastTestStatus("success");
        setSmtpLastTestedAt(new Date().toISOString());
        setSmtpLastTestMessage(res.message);
        toast.success(res.message);
      } else {
        setSmtpLastTestStatus("error");
        setSmtpLastTestedAt(new Date().toISOString());
        setSmtpLastTestMessage(res.message);
        toast.error(res.message);
      }
    } catch (err: any) {
      setSmtpLogs((prev) => [...prev, `❌ Exception: ${err.message || String(err)}`]);
      setSmtpLastTestStatus("error");
      setSmtpLastTestMessage(err.message || "Connection failed");
      toast.error(err.message || "SMTP test failed");
    } finally {
      setSmtpTesting(false);
    }
  };

  return (
    <div className="page w-full max-w-full min-w-0">
      <PageHeader
        title="System &amp; Access Control Settings"
        description="Manage company details, feature-based roles and permissions, module access, master data, and outbound HRM integration."
      />

      <Tabs value={activeTab} onValueChange={handleTabChange} className="space-y-4 w-full max-w-full min-w-0">
        {/* Responsive Horizontal Scroll Tabs Container */}
        <div className="relative w-full max-w-full group">
          {/* Left Edge Fading Mask & Floating Chevron */}
          <div
            className={cn(
              "pointer-events-none absolute left-0 top-0 bottom-0 w-12 bg-linear-to-r from-background via-background/90 to-transparent z-20 transition-all duration-200 flex items-center justify-start pl-0.5",
              canScrollTabsLeft ? "opacity-100" : "opacity-0 pointer-events-none",
            )}
          >
            <button
              type="button"
              onClick={() => scrollTabs("left")}
              disabled={!canScrollTabsLeft}
              aria-label="Scroll tabs left"
              className="pointer-events-auto size-7 rounded-full bg-card/95 hover:bg-card border border-border hover:border-copper shadow-md text-foreground hover:text-copper flex items-center justify-center transition-all hover:scale-105 active:scale-95 cursor-pointer backdrop-blur-xs"
            >
              <ChevronLeft className="size-4" />
            </button>
          </div>

          {/* Scrollable Tabs List Viewport */}
          <div
            ref={tabsScrollRef}
            className="w-full max-w-full overflow-x-auto no-scrollbar scroll-smooth border-b border-border px-0"
          >
            <TabsList className="mb-0 border-b-0 inline-flex w-max gap-4 p-0 h-9">
              {/* Rendered from the feature registry: each feature contributing a
                  settings tab appears here when the actor may open it. */}
              {availableTabs.map((tab) => (
                <TabsTrigger
                  key={tab.tab}
                  value={tab.tab}
                  className="flex items-center gap-1.5 shrink-0 whitespace-nowrap"
                >
                  {hasIcon(tab.icon) && <Icon name={tab.icon} className="size-3.5 text-copper" />}
                  <span>
                    {tab.label}
                    {tabCounts[tab.tab] !== undefined ? ` (${tabCounts[tab.tab]})` : ""}
                  </span>
                </TabsTrigger>
              ))}
            </TabsList>
          </div>

          {/* Right Edge Fading Mask & Floating Chevron */}
          <div
            className={cn(
              "pointer-events-none absolute right-0 top-0 bottom-0 w-12 bg-linear-to-l from-background via-background/90 to-transparent z-20 transition-all duration-200 flex items-center justify-end pr-0.5",
              canScrollTabsRight ? "opacity-100" : "opacity-0 pointer-events-none",
            )}
          >
            <button
              type="button"
              onClick={() => scrollTabs("right")}
              disabled={!canScrollTabsRight}
              aria-label="Scroll tabs right"
              className="pointer-events-auto size-7 rounded-full bg-card/95 hover:bg-card border border-border hover:border-copper shadow-md text-foreground hover:text-copper flex items-center justify-center transition-all hover:scale-105 active:scale-95 cursor-pointer backdrop-blur-xs"
            >
              <ChevronRight className="size-4" />
            </button>
          </div>
        </div>

        {/* 1. COMPANY & BRANDING */}
        {canViewCompany && (
          <TabsContent value="company" className="space-y-4 w-full max-w-full min-w-0">
            <Card className="shadow-none">
              <CardHeader className="pb-3">
                <CardTitle className="text-sm font-semibold">Organization Profile</CardTitle>
                <CardDescription className="text-xs">
                  Company identity shown on applicant portal and offer letters
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-4 text-xs max-w-xl">
                <div className="space-y-1.5">
                  <label className="field-label">Organization Name</label>
                  <Input
                    value={orgName}
                    onChange={(e) => setOrgName(e.target.value)}
                    className="h-8 text-xs"
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="field-label">Careers Portal Subdomain</label>
                  <Input
                    value={careersDomain}
                    onChange={(e) => setCareersDomain(e.target.value)}
                    className="h-8 text-xs"
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="field-label">Primary Timezone</label>
                  <Input
                    value={timezone}
                    onChange={(e) => setTimezone(e.target.value)}
                    className="h-8 text-xs"
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="field-label">Default Reporting Currency</label>
                  <select
                    value={defaultCurrency}
                    onChange={(e) => setDefaultCurrency(e.target.value)}
                    className="h-8 w-full rounded-xs border border-border bg-card px-2.5 text-xs text-foreground focus:border-ring"
                  >
                    {currenciesList.length === 0 ? (
                      <option value="USD">USD ($)</option>
                    ) : (
                      currenciesList.map((c) => (
                        <option key={c.id} value={c.code}>
                          {c.code} — {c.name}
                        </option>
                      ))
                    )}
                  </select>
                </div>

                <div className="pt-2">
                  <Button
                    size="xs"
                    variant="accent"
                    disabled={isSavingOrg}
                    onClick={handleSaveOrg}
                    className="gap-1"
                  >
                    {isSavingOrg ? <Loader2 className="size-3 animate-spin" /> : null}
                    <span>Save Organization Profile</span>
                  </Button>
                </div>
              </CardContent>
            </Card>
          </TabsContent>
        )}

        {/* 2. DYNAMIC RBAC ROLES & PERMISSIONS */}
        <TabsContent value="rbac" className="space-y-6 w-full max-w-full min-w-0">
          {!canViewRBAC ? (
            <AccessDenied
              errorCode="403"
              title="Access Denied"
              description="You do not have permission to view or manage roles and permissions."
              showBackHome={false}
            />
          ) : (
            <>
              {/* Action Bar */}
              <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 p-4 bg-card rounded-xs border border-border">
                <div className="space-y-0.5">
                  <h2 className="text-sm font-semibold text-foreground flex items-center gap-1.5">
                    <ShieldCheck className="size-4 text-copper" />
                    <span>Feature-Based Roles &amp; Permissions</span>
                  </h2>
                  <p className="text-xs text-muted-foreground">
                    {catalogue.permissions.length} permissions across {catalogue.features.length}{" "}
                    platform features. Every action a role may perform is configured here — no code
                    changes required.
                  </p>
                </div>
                <Can permission="roles.create">
                  <Button
                    size="sm"
                    variant="accent"
                    onClick={handleOpenCreateRole}
                    className="gap-1 text-xs shrink-0"
                  >
                    <Plus className="size-3.5" />
                    <span>Create Custom Role</span>
                  </Button>
                </Can>
              </div>

              {/* Roles Cards Grid */}
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                {rolesList.map((r) => (
                  <Card
                    key={r.id}
                    className="shadow-none border border-border hover:border-copper/60 transition-all flex flex-col justify-between"
                  >
                    <CardHeader className="pb-2">
                      <div className="flex items-start justify-between gap-2">
                        <div>
                          <div className="flex items-center gap-2">
                            <CardTitle className="text-sm font-semibold">{r.name}</CardTitle>
                            {r.isSuperAdmin ? (
                              <Badge
                                variant="secondary"
                                className="text-[9px] uppercase tracking-wider bg-copper/10 text-copper border-copper/30 flex items-center gap-1"
                              >
                                <Lock className="size-2.5" />
                                <span>Super Admin</span>
                              </Badge>
                            ) : (
                              <Badge
                                variant={r.isSystem ? "secondary" : "soft-success"}
                                className="text-[9px] uppercase tracking-wider"
                              >
                                {r.isSystem ? "Built-in" : "Custom"}
                              </Badge>
                            )}
                          </div>
                          <div className="text-[10px] text-muted-foreground mt-0.5">
                            slug: {r.slug}
                          </div>
                        </div>
                        <Badge variant="outline" className="text-[10px] font-medium">
                          {r.userCount || 0} {(r.userCount || 0) === 1 ? "user" : "users"}
                        </Badge>
                      </div>
                      <CardDescription className="text-xs line-clamp-2 mt-1">
                        {r.isSuperAdmin
                          ? "Unrestricted access to every feature and action. Not editable or deletable."
                          : r.description || "Custom defined role with tailored permissions."}
                      </CardDescription>
                    </CardHeader>

                    <CardContent className="space-y-3 pt-2 text-xs">
                      <div className="flex items-center justify-between text-[11px] text-muted-foreground border-t border-border pt-2">
                        <span>
                          <strong className="text-foreground">
                            {r.permissions.length}
                          </strong>{" "}
                          of {catalogue.permissions.length} permissions granted
                        </span>
                        <Badge variant="outline" className="text-[10px]">
                          {r.badge || "Role"}
                        </Badge>
                      </div>

                      <div className="flex items-center justify-end gap-1.5 pt-1 border-t border-border/60">
                        {r.isSuperAdmin ? (
                          <Badge
                            variant="outline"
                            className="text-[10px] text-muted-foreground gap-1 h-7 px-2 bg-muted/30"
                          >
                            <Lock className="size-3 text-copper" />
                            <span>Read-Only</span>
                          </Badge>
                        ) : (
                          <>
                            <Can permission="roles.update">
                              <Button
                                size="xs"
                                variant="outline"
                                onClick={() => handleOpenEditRole(r)}
                                className="h-7 px-2.5 text-xs gap-1"
                              >
                                <Edit2 className="size-3" />
                                <span>Edit Permissions</span>
                              </Button>
                            </Can>

                            {!r.isSystem && (
                              <Can permission="roles.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteRole(r)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive hover:bg-destructive/10"
                                  title="Delete Custom Role"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            )}
                          </>
                        )}
                      </div>
                    </CardContent>
                  </Card>
                ))}
              </div>

              {/* Live RBAC Permission Matrix */}
              <Card className="shadow-none overflow-hidden">
                <CardHeader className="pb-3 border-b border-border bg-muted/20">
                  <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-3">
                    <div>
                      <CardTitle className="text-sm font-semibold flex items-center gap-2">
                        <ShieldCheck className="size-4 text-copper" />
                        <span>Interactive Feature Permission Matrix</span>
                      </CardTitle>
                      <CardDescription className="text-xs">
                        Click any cell to grant or revoke that action for a role, instantly and
                        everywhere — navigation, pages, APIs, and CRUD operations.
                      </CardDescription>
                    </div>
                    <div className="flex items-center gap-2.5">
                      <select
                        value={matrixFeatureFilter}
                        onChange={(e) => setMatrixFeatureFilter(e.target.value)}
                        className="h-7 text-xs rounded-xs border border-border bg-card px-2 text-foreground focus:outline-none focus:ring-1 focus:ring-copper"
                      >
                        <option value="all">All features</option>
                        {catalogue.features.map((f) => (
                          <option key={f.key} value={f.key}>
                            {f.name}
                          </option>
                        ))}
                      </select>
                      <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                        <span className="flex items-center gap-1">
                          <Check className="size-3 text-success font-bold" />
                          <span>Granted</span>
                        </span>
                        <span>•</span>
                        <span className="flex items-center gap-1">
                          <X className="size-3 text-muted-foreground/40 font-bold" />
                          <span>Denied</span>
                        </span>
                      </div>
                    </div>
                  </div>
                </CardHeader>
                <CardContent className="p-0 overflow-x-auto">
                  <Table>
                    <TableHeader>
                      <TableRow className="text-xs bg-muted/40">
                        <TableHead className="w-80 min-w-70">Feature &amp; Action</TableHead>
                        {rolesList.map((r) => (
                          <TableHead key={r.id} className="text-center min-w-30">
                            <div>
                              <span className="font-semibold text-foreground text-xs block">
                                {r.name}
                              </span>
                              <span className="text-[9px] text-muted-foreground">
                                {r.isSuperAdmin
                                  ? "unrestricted"
                                  : r.isSystem
                                    ? "built-in"
                                    : "custom"}
                              </span>
                            </div>
                          </TableHead>
                        ))}
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {catalogue.groups.map((group) => {
                        const groupFeatures = catalogue.features.filter(
                          (f) =>
                            f.group === group.key &&
                            (matrixFeatureFilter === "all" || f.key === matrixFeatureFilter),
                        );
                        if (groupFeatures.length === 0) return null;

                        return (
                          <React.Fragment key={group.key}>
                            <TableRow className="bg-muted/60 text-xs font-semibold text-foreground">
                              <TableCell
                                colSpan={rolesList.length + 1}
                                className="py-1.5 text-copper uppercase tracking-wider text-[10px]"
                              >
                                {group.name}
                              </TableCell>
                            </TableRow>

                            {groupFeatures.map((feature) => (
                              <React.Fragment key={feature.key}>
                                <TableRow className="text-xs bg-muted/20">
                                  <TableCell
                                    colSpan={rolesList.length + 1}
                                    className="py-1.5 font-semibold text-foreground"
                                  >
                                    <div className="flex items-center gap-1.5">
                                      <span>{feature.name}</span>
                                      <span className="text-[10px] font-normal text-muted-foreground">
                                        {feature.key}
                                      </span>
                                    </div>
                                  </TableCell>
                                </TableRow>

                                {feature.actions.map((perm) => (
                                  <TableRow
                                    key={perm.key}
                                    className="text-xs hover:bg-muted/30 transition-colors"
                                  >
                                    <TableCell className="font-medium">
                                      <div className="flex items-center gap-1.5">
                                        <span className="font-semibold text-foreground text-xs">
                                          {perm.label}
                                        </span>
                                        {perm.sensitive && (
                                          <Badge
                                            variant="outline"
                                            className="text-[9px] uppercase tracking-wide text-copper border-copper/30"
                                          >
                                            Sensitive
                                          </Badge>
                                        )}
                                      </div>
                                      <div className="text-[10px] text-muted-foreground leading-tight mt-0.5">
                                        {perm.description}
                                      </div>
                                      <div className="text-[10px] text-muted-foreground/70 mt-0.5">
                                        {perm.key}
                                      </div>
                                    </TableCell>

                                    {rolesList.map((r) => {
                                      const isGranted =
                                        r.isSuperAdmin || r.permissions.includes(perm.key);
                                      const isCellToggling =
                                        togglingKey === `${r.id}_${perm.key}`;

                                      if (r.isSuperAdmin) {
                                        return (
                                          <TableCell key={r.id} className="text-center">
                                            <div
                                              className="inline-flex items-center justify-center size-6 rounded-xs bg-copper/10 border border-copper/30 text-copper cursor-not-allowed"
                                              title="Super Admin retains full access and is permanently read-only."
                                            >
                                              <Check className="size-3.5 stroke-[2.5]" />
                                            </div>
                                          </TableCell>
                                        );
                                      }

                                      return (
                                        <TableCell key={r.id} className="text-center">
                                          <button
                                            onClick={() => handleMatrixToggle(r, perm.key)}
                                            disabled={isCellToggling || !canAssignPermissions}
                                            title={
                                              canAssignPermissions
                                                ? `Click to ${isGranted ? "revoke" : "grant"} ${perm.label} for ${r.name}`
                                                : "You do not have permission to change role permissions"
                                            }
                                            className={`inline-flex items-center justify-center size-6 rounded-xs border transition-all ${
                                              isGranted
                                                ? "bg-success/15 border-success/40 text-success hover:bg-destructive/20 hover:text-destructive hover:border-destructive"
                                                : "bg-muted/40 border-border text-muted-foreground/30 hover:bg-success/20 hover:text-success hover:border-success"
                                            } ${canAssignPermissions ? "" : "cursor-not-allowed opacity-70"}`}
                                          >
                                            {isCellToggling ? (
                                              <Loader2 className="size-3 animate-spin text-copper" />
                                            ) : isGranted ? (
                                              <Check className="size-3.5 stroke-[2.5]" />
                                            ) : (
                                              <X className="size-3 stroke-2" />
                                            )}
                                          </button>
                                        </TableCell>
                                      );
                                    })}
                                  </TableRow>
                                ))}
                              </React.Fragment>
                            ))}
                          </React.Fragment>
                        );
                      })}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>
            </>
          )}
        </TabsContent>

        {/* 3. USERS & DIRECTORY */}
        {canViewUsers && (
          <TabsContent value="users" className="space-y-4">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Manage internal team access. A user&apos;s effective permissions are the union of
                every role assigned to them.
              </div>
              <Can permission="users.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setUserModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <UserPlus className="size-3.5" />
                  <span>Invite User</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>User &amp; Email</TH>
                  <TH>Assigned Roles</TH>
                  <TH>Department</TH>
                  <TH>Status</TH>
                  <TH>Created Date</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {usersList.length === 0 ? (
                    <EmptyRow colSpan={6}>No user accounts found.</EmptyRow>
                  ) : (
                    usersList.map((u) => {
                      const isSelf = u.id === currentUserId;
                      return (
                        <TR key={u.id}>
                          <TD>
                            <div>
                              <span className="font-semibold text-foreground text-xs block">
                                {u.name}
                                {isSelf && (
                                  <span className="ml-1.5 text-[10px] text-muted-foreground font-normal">
                                    (you)
                                  </span>
                                )}
                              </span>
                              <span className="text-[11px] text-muted-foreground">{u.email}</span>
                            </div>
                          </TD>

                          <TD>
                            <div className="flex flex-wrap items-center gap-1">
                              {(u.roleNames?.length ? u.roleNames : ["No role assigned"]).map(
                                (name: string) => (
                                  <Badge
                                    key={name}
                                    variant={u.isSuperAdmin ? "secondary" : "outline"}
                                    className={
                                      u.isSuperAdmin
                                        ? "text-[10px] bg-copper/10 text-copper border-copper/30 gap-1"
                                        : "text-[10px]"
                                    }
                                  >
                                    {u.isSuperAdmin && <Lock className="size-2.5" />}
                                    <span>{name}</span>
                                  </Badge>
                                ),
                              )}

                              {canAssignUserRoles && !isSelf && (
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenAssignRoles(u)}
                                  className="h-6 px-1.5 text-[10px] text-copper hover:bg-copper/10 gap-1"
                                  title="Assign roles"
                                >
                                  <Edit2 className="size-3" />
                                  <span>Manage</span>
                                </Button>
                              )}
                            </div>
                          </TD>

                          <TD>
                            <span className="text-muted-foreground text-xs">
                              {u.departmentName || "General Operations"}
                            </span>
                          </TD>

                          <TD>
                            {u.isActive ? (
                              <Badge variant="soft-success" className="text-[10px]">
                                Active
                              </Badge>
                            ) : (
                              <Badge variant="outline" className="text-[10px] text-muted-foreground">
                                Deactivated
                              </Badge>
                            )}
                          </TD>

                          <TD className="text-muted-foreground text-xs">
                            {new Date(u.createdAt).toLocaleDateString()}
                          </TD>

                          <TD align="right">
                            {isSelf ? (
                              <span className="text-[10px] text-muted-foreground italic">
                                Self-management disabled
                              </span>
                            ) : (
                              <div className="flex items-center justify-end gap-1">
                                <Can permission="users.manage_status">
                                  <Button
                                    size="xs"
                                    variant="ghost"
                                    onClick={() => handleToggleUserActive(u.id, u.isActive)}
                                    className="h-7 px-2 text-xs text-muted-foreground hover:text-foreground"
                                  >
                                    {u.isActive ? "Deactivate" : "Activate"}
                                  </Button>
                                </Can>
                                <Can permission="users.delete">
                                  <Button
                                    size="xs"
                                    variant="ghost"
                                    onClick={() => handleDeleteUser(u.id, u.name)}
                                    className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                  >
                                    <Trash2 className="size-3.5" />
                                  </Button>
                                </Can>
                              </div>
                            )}
                          </TD>
                        </TR>
                      );
                    })
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 3b. FEATURE ACCESS */}
        {canViewFeatures && (
          <TabsContent value="features" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 p-4 bg-card rounded-xs border border-border">
              <div className="space-y-0.5">
                <h2 className="text-sm font-semibold text-foreground flex items-center gap-1.5">
                  <ShieldCheck className="size-4 text-copper" />
                  <span>Organization Feature Access</span>
                </h2>
                <p className="text-xs text-muted-foreground">
                  Switch whole modules on or off. A disabled feature disappears from navigation and
                  its routes, APIs, and actions are rejected for every role.
                </p>
              </div>
            </div>

            {FEATURE_GROUPS.map((group) => {
              const rows = featureRows.filter((f) => f.group === group.key);
              if (rows.length === 0) return null;

              return (
                <Card key={group.key} className="shadow-none">
                  <CardHeader className="pb-3 border-b border-border bg-muted/20">
                    <CardTitle className="text-sm font-semibold text-copper uppercase tracking-wider text-[11px]">
                      {group.name}
                    </CardTitle>
                    <CardDescription className="text-xs">{group.description}</CardDescription>
                  </CardHeader>
                  <CardContent className="p-0">
                    <TableShell>
                      <DTable>
                        <THead>
                          <TH>Feature</TH>
                          <TH>Permissions</TH>
                          <TH align="right">Status</TH>
                        </THead>
                        <TBody>
                          {rows.map((f) => (
                            <TR key={f.key}>
                              <TD>
                                <div>
                                  <span className="font-semibold text-foreground text-xs block">
                                    {f.name}
                                  </span>
                                  <span className="text-[11px] text-muted-foreground">
                                    {f.description}
                                  </span>
                                </div>
                              </TD>
                              <TD>
                                <Badge variant="outline" className="text-[10px]">
                                  {f.permissionCount} actions
                                </Badge>
                              </TD>
                              <TD align="right">
                                {f.alwaysEnabled ? (
                                  <Badge
                                    variant="secondary"
                                    className="text-[10px] bg-copper/10 text-copper border-copper/30 gap-1"
                                  >
                                    <Lock className="size-2.5" />
                                    <span>Core module</span>
                                  </Badge>
                                ) : (
                                  <Button
                                    size="xs"
                                    variant={f.isEnabled ? "outline" : "ghost"}
                                    disabled={
                                      togglingFeature === f.key || !canToggleFeatures
                                    }
                                    onClick={() => handleToggleFeature(f)}
                                    className="h-7 px-2.5 text-xs gap-1"
                                  >
                                    {togglingFeature === f.key ? (
                                      <Loader2 className="size-3 animate-spin text-copper" />
                                    ) : f.isEnabled ? (
                                      <Check className="size-3 text-success" />
                                    ) : (
                                      <X className="size-3 text-muted-foreground" />
                                    )}
                                    <span>{f.isEnabled ? "Enabled" : "Disabled"}</span>
                                  </Button>
                                )}
                              </TD>
                            </TR>
                          ))}
                        </TBody>
                      </DTable>
                    </TableShell>
                  </CardContent>
                </Card>
              );
            })}
          </TabsContent>
        )}

        {/* 3c. AUDIT TRAIL */}
        {canViewAudit && (
          <TabsContent value="audit" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
              <div className="text-xs text-muted-foreground">
                Every permission change, denied request, and data modification recorded by the
                system.
              </div>
              <div className="flex items-center gap-2">
                <Input
                  value={auditSearch}
                  onChange={(e) => setAuditSearch(e.target.value)}
                  placeholder="Filter by action, entity, or actor..."
                  className="h-8 w-64 text-xs"
                />
                <Button
                  size="sm"
                  variant="outline"
                  onClick={loadAudit}
                  disabled={auditLoading}
                  className="gap-1 text-xs"
                >
                  {auditLoading ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <RefreshCw className="size-3.5" />
                  )}
                  <span>Refresh</span>
                </Button>
              </div>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Action</TH>
                  <TH>Entity</TH>
                  <TH>Actor</TH>
                  <TH align="right">When</TH>
                </THead>
                <TBody>
                  {auditRows.length === 0 ? (
                    <EmptyRow colSpan={4}>
                      {auditLoading ? "Loading audit events..." : "No audit events recorded yet."}
                    </EmptyRow>
                  ) : (
                    auditRows.map((log) => (
                      <TR key={log.id}>
                        <TD>
                          <span className="font-semibold text-foreground text-xs block">
                            {log.action}
                          </span>
                        </TD>
                        <TD>
                          <span className="text-[11px] text-muted-foreground">
                            {log.entityType} · {log.entityId}
                          </span>
                        </TD>
                        <TD>
                          <span className="text-xs text-foreground">
                            {log.actorName || "System"}
                          </span>
                          {log.actorEmail && (
                            <span className="block text-[10px] text-muted-foreground">
                              {log.actorEmail}
                            </span>
                          )}
                        </TD>
                        <TD align="right" className="text-muted-foreground text-xs">
                          {new Date(log.createdAt).toLocaleString()}
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 4. DEPARTMENTS */}
        {canViewDepts && (
          <TabsContent value="departments" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Define functional departments for requisitions and workforce allocation.
              </div>
              <Can permission="departments.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setDeptModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Department</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Department Name</TH>
                  <TH>Department Code</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {departments.length === 0 ? (
                    <EmptyRow colSpan={3}>No departments found.</EmptyRow>
                  ) : (
                    departments.map((dept) => (
                      <TR key={dept.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{dept.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {dept.code}
                          </Badge>
                        </TD>
                        <TD align="right">
                          <Can anyOf={["departments.update", "departments.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="departments.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditDept(dept)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="departments.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteDept(dept.id, dept.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 5. LOCATIONS */}
        {canViewLocations && (
          <TabsContent value="locations" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Configure global office locations and hiring hubs.
              </div>
              <Can permission="locations.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setLocModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Office Hub</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Office Location</TH>
                  <TH>City</TH>
                  <TH>Country</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {locations.length === 0 ? (
                    <EmptyRow colSpan={4}>No office hubs found.</EmptyRow>
                  ) : (
                    locations.map((loc) => (
                      <TR key={loc.id}>
                        <TD>
                          <div className="flex items-center gap-1.5 font-semibold text-foreground text-xs">
                            <MapPin className="size-3.5 text-copper shrink-0" />
                            <span>{loc.name}</span>
                          </div>
                        </TD>
                        <TD>
                          <span className="text-xs text-foreground">{loc.city}</span>
                        </TD>
                        <TD>
                          <span className="text-xs text-muted-foreground">{loc.country}</span>
                        </TD>
                        <TD align="right">
                          <Can anyOf={["locations.update", "locations.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="locations.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditLoc(loc)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="locations.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteLoc(loc.id, loc.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 5a. CURRENCIES MASTER */}
        {canViewCurrencies && (
          <TabsContent value="currencies" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Configure global transaction and compensation currencies used across job requisitions and candidate offer letters.
              </div>
              <Can permission="currencies.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setCurrencyModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Currency</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Currency Code</TH>
                  <TH>Symbol</TH>
                  <TH>Full Name</TH>
                  <TH>Default Status</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {currenciesList.length === 0 ? (
                    <EmptyRow colSpan={5}>No currencies found.</EmptyRow>
                  ) : (
                    currenciesList.map((c) => (
                      <TR key={c.id}>
                        <TD mono>
                          <span className="font-semibold text-xs text-foreground uppercase">{c.code}</span>
                        </TD>
                        <TD>
                          <Badge variant="outline" className="text-[11px] font-bold border-copper/30 text-copper">
                            {c.symbol}
                          </Badge>
                        </TD>
                        <TD>
                          <span className="text-xs text-foreground font-medium">{c.name}</span>
                        </TD>
                        <TD>
                          {c.isDefault ? (
                            <Badge variant="soft-success" className="text-[10px]">
                              Default Base
                            </Badge>
                          ) : (
                            <span className="text-[11px] text-muted-foreground">—</span>
                          )}
                        </TD>
                        <TD align="right">
                          <Can anyOf={["currencies.update", "currencies.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="currencies.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditCurrency(c)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="currencies.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteCurrency(c.id, c.code)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 5b. PAY FREQUENCIES MASTER */}
        {canViewPayFrequencies && (
          <TabsContent value="pay-frequencies" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Define payroll and salary frequency intervals (e.g. Annual, Monthly, Hourly, Bi-Weekly) for compensation packaging.
              </div>
              <Can permission="pay-frequencies.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setPayFreqModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Pay Frequency</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Pay Frequency</TH>
                  <TH>Slug / Code</TH>
                  <TH>Description</TH>
                  <TH>Default</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {payFrequenciesList.length === 0 ? (
                    <EmptyRow colSpan={5}>No pay frequencies found.</EmptyRow>
                  ) : (
                    payFrequenciesList.map((f) => (
                      <TR key={f.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{f.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {f.slug}
                          </Badge>
                        </TD>
                        <TD className="text-muted-foreground">{f.description || "—"}</TD>
                        <TD>
                          {f.isDefault ? (
                            <Badge variant="soft-success" className="text-[10px]">
                              Default
                            </Badge>
                          ) : (
                            <span className="text-[11px] text-muted-foreground">—</span>
                          )}
                        </TD>
                        <TD align="right">
                          <Can anyOf={["pay-frequencies.update", "pay-frequencies.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="pay-frequencies.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditPayFreq(f)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="pay-frequencies.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeletePayFreq(f.id, f.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 5c. REQUISITION STATUSES MASTER */}
        {canViewJobStatuses && (
          <TabsContent value="job-statuses" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Manage requisition workflow lifecycle states and their visual badge styling across hiring pipelines.
              </div>
              <Can permission="job-statuses.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setJobStatusModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Requisition Status</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Status Name</TH>
                  <TH>Slug / Code</TH>
                  <TH>Badge Styling</TH>
                  <TH>Description</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {jobStatusesList.length === 0 ? (
                    <EmptyRow colSpan={5}>No requisition statuses found.</EmptyRow>
                  ) : (
                    jobStatusesList.map((s) => (
                      <TR key={s.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{s.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {s.slug}
                          </Badge>
                        </TD>
                        <TD>
                          <Badge variant={(s.badgeVariant as any) || "secondary"} className="text-[10px]">
                            {s.name}
                          </Badge>
                        </TD>
                        <TD className="text-muted-foreground">{s.description || "—"}</TD>
                        <TD align="right">
                          <Can anyOf={["job-statuses.update", "job-statuses.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="job-statuses.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditJobStatus(s)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="job-statuses.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteJobStatus(s.id, s.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 5d. INTERVIEW ROUND TYPES MASTER */}
        {canViewInterviewTypes && (
          <TabsContent value="interview-types" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Configure standard interview round templates and default durations for candidate interview scheduling.
              </div>
              <Can permission="interview-types.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setInterviewTypeModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Interview Round Type</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Round Type Name</TH>
                  <TH>Slug / Code</TH>
                  <TH>Default Duration</TH>
                  <TH>Description</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {interviewTypesList.length === 0 ? (
                    <EmptyRow colSpan={5}>No interview round types found.</EmptyRow>
                  ) : (
                    interviewTypesList.map((t) => (
                      <TR key={t.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{t.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {t.slug}
                          </Badge>
                        </TD>
                        <TD>
                          <span className="text-xs text-foreground font-medium">
                            {t.defaultDurationMinutes || 45} mins
                          </span>
                        </TD>
                        <TD className="text-muted-foreground">{t.description || "—"}</TD>
                        <TD align="right">
                          <Can anyOf={["interview-types.update", "interview-types.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="interview-types.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditInterviewType(t)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="interview-types.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteInterviewType(t.id, t.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 5e. BENEFIT CATEGORIES MASTER */}
        {canViewBenefitCategories && (
          <TabsContent value="benefit-categories" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Manage company perk and benefit taxonomy categories (e.g. Healthcare, Financial, Paid Time Off, Growth).
              </div>
              <Can permission="benefit-categories.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setBenefitCatModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Benefit Category</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Category Name</TH>
                  <TH>Slug / Code</TH>
                  <TH>Description</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {benefitCategoriesList.length === 0 ? (
                    <EmptyRow colSpan={4}>No benefit categories found.</EmptyRow>
                  ) : (
                    benefitCategoriesList.map((b) => (
                      <TR key={b.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{b.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {b.slug}
                          </Badge>
                        </TD>
                        <TD className="text-muted-foreground">{b.description || "—"}</TD>
                        <TD align="right">
                          <Can anyOf={["benefit-categories.update", "benefit-categories.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="benefit-categories.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditBenefitCat(b)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="benefit-categories.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteBenefitCat(b.id, b.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 6. WORK MODES */}
        {canViewWorkModes && (
          <TabsContent value="work-modes" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Define dynamic work arrangement options (e.g. Hybrid, Fully Remote, On-Site) for job requisitions.
              </div>
              <Can permission="work-modes.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setWorkModeModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Work Mode</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Work Mode</TH>
                  <TH>Slug / Code</TH>
                  <TH>Description</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {workModesList.length === 0 ? (
                    <EmptyRow colSpan={4}>No work modes found.</EmptyRow>
                  ) : (
                    workModesList.map((wm) => (
                      <TR key={wm.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{wm.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {wm.slug}
                          </Badge>
                        </TD>
                        <TD className="text-muted-foreground">{wm.description || "—"}</TD>
                        <TD align="right">
                          <Can anyOf={["work-modes.update", "work-modes.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="work-modes.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditWorkMode(wm)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="work-modes.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteWorkMode(wm.id, wm.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 7. EMPLOYMENT TYPES */}
        {canViewEmpTypes && (
          <TabsContent value="employment-types" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Manage contract and employment classifications (e.g. Full-time Permanent, Contract, Internship) available during job creation.
              </div>
              <Can permission="employment-types.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setEmpTypeModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Employment Type</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Employment Type</TH>
                  <TH>Slug / Code</TH>
                  <TH>Description</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {employmentTypesList.length === 0 ? (
                    <EmptyRow colSpan={4}>No employment types found.</EmptyRow>
                  ) : (
                    employmentTypesList.map((et) => (
                      <TR key={et.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{et.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {et.slug}
                          </Badge>
                        </TD>
                        <TD className="text-muted-foreground">{et.description || "—"}</TD>
                        <TD align="right">
                          <Can anyOf={["employment-types.update", "employment-types.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="employment-types.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditEmpType(et)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="employment-types.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteEmpType(et.id, et.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 7b. EXPERIENCE LEVELS */}
        {canViewExpLevels && (
          <TabsContent value="experience-levels" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Configure seniority tiers and minimum experience year ranges used in requisitions and candidate scorecards.
              </div>
              <Can permission="experience-levels.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setExpLevelModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Experience Level</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Experience Level</TH>
                  <TH>Slug / Code</TH>
                  <TH>Years Range</TH>
                  <TH>Description</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {experienceLevelsList.length === 0 ? (
                    <EmptyRow colSpan={5}>No experience levels found.</EmptyRow>
                  ) : (
                    experienceLevelsList.map((exp) => (
                      <TR key={exp.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{exp.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {exp.slug}
                          </Badge>
                        </TD>
                        <TD>
                          <span className="text-xs text-foreground font-medium">
                            {exp.minYears} – {exp.maxYears} yrs
                          </span>
                        </TD>
                        <TD className="text-muted-foreground">{exp.description || "—"}</TD>
                        <TD align="right">
                          <Can anyOf={["experience-levels.update", "experience-levels.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="experience-levels.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditExpLevel(exp)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="experience-levels.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteExpLevel(exp.id, exp.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 7c. EDUCATION REQUIREMENTS */}
        {canViewEduLevels && (
          <TabsContent value="education-levels" className="space-y-4 w-full max-w-full min-w-0">
            <div className="flex items-center justify-between">
              <div className="text-xs text-muted-foreground">
                Define educational degrees and qualification classifications for job openings.
              </div>
              <Can permission="education-levels.create">
                <Button
                  size="sm"
                  variant="accent"
                  onClick={() => setEduLevelModalOpen(true)}
                  className="gap-1 text-xs"
                >
                  <Plus className="size-3.5" />
                  <span>Add Education Requirement</span>
                </Button>
              </Can>
            </div>

            <TableShell>
              <DTable>
                <THead>
                  <TH>Education Level</TH>
                  <TH>Slug / Code</TH>
                  <TH>Description</TH>
                  <TH align="right">Actions</TH>
                </THead>
                <TBody>
                  {educationLevelsList.length === 0 ? (
                    <EmptyRow colSpan={4}>No education requirements found.</EmptyRow>
                  ) : (
                    educationLevelsList.map((edu) => (
                      <TR key={edu.id}>
                        <TD>
                          <span className="font-semibold text-xs text-foreground">{edu.name}</span>
                        </TD>
                        <TD mono>
                          <Badge variant="outline" className="text-[10px] border-copper/30 text-copper">
                            {edu.slug}
                          </Badge>
                        </TD>
                        <TD className="text-muted-foreground">{edu.description || "—"}</TD>
                        <TD align="right">
                          <Can anyOf={["education-levels.update", "education-levels.delete"]}>
                            <div className="flex items-center justify-end gap-1">
                              <Can permission="education-levels.update">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleOpenEditEduLevel(edu)}
                                  className="h-7 w-7 p-0 text-muted-foreground hover:text-foreground"
                                >
                                  <Edit2 className="size-3.5" />
                                </Button>
                              </Can>
                              <Can permission="education-levels.delete">
                                <Button
                                  size="xs"
                                  variant="ghost"
                                  onClick={() => handleDeleteEduLevel(edu.id, edu.name)}
                                  className="h-7 w-7 p-0 text-destructive/70 hover:text-destructive"
                                >
                                  <Trash2 className="size-3.5" />
                                </Button>
                              </Can>
                            </div>
                          </Can>
                        </TD>
                      </TR>
                    ))
                  )}
                </TBody>
              </DTable>
            </TableShell>
          </TabsContent>
        )}

        {/* 8. HRM & INTEGRATIONS */}
        {canViewSDK && (
          <TabsContent value="integrations" className="space-y-4 w-full max-w-full min-w-0">
            <Card className="shadow-none">
              <CardHeader className="pb-3">
                <div className="flex items-center justify-between">
                  <div>
                    <CardTitle className="text-sm font-semibold">HRM &amp; Payroll Handover</CardTitle>
                    <CardDescription className="text-xs">
                      Where accepted offers are pushed when a candidate is onboarded
                    </CardDescription>
                  </div>
                  <Badge variant={hrmWebhookUrl ? "soft-success" : "outline"} className="text-[10px]">
                    {hrmWebhookUrl ? "Endpoint configured" : "Not configured"}
                  </Badge>
                </div>
              </CardHeader>
              <CardContent className="space-y-4">
                <div className="space-y-1.5">
                  <label className="field-label">HRM Webhook URL</label>
                  <Input
                    value={hrmWebhookUrl}
                    onChange={(e) => setHrmWebhookUrl(e.target.value)}
                    placeholder="https://hrm.example.com/api/v1/recruitment-webhook"
                    disabled={!access.can("integrations.update")}
                    className="h-8 text-xs"
                  />
                  <p className="text-[10px] text-muted-foreground">
                    Offers synchronized from the Offers module are delivered here. Sending a
                    candidate to HRM requires the{" "}
                    <span className="text-foreground font-medium">offers.sync_hrm</span> permission.
                  </p>
                </div>

                <div className="p-3 bg-muted/40 rounded-xs border border-border space-y-1.5">
                  <div className="text-[11px] font-semibold text-foreground">Onboarding payload</div>
                  <p className="text-[10px] text-muted-foreground leading-relaxed">
                    Candidate identity, designation, department, joining date, and the accepted
                    compensation package. Compensation fields are only readable in-app by roles
                    holding{" "}
                    <span className="text-foreground font-medium">offers.view_compensation</span>.
                  </p>
                </div>

                <Can permission="integrations.update">
                  <div className="flex justify-end">
                    <Button
                      size="sm"
                      variant="accent"
                      disabled={isSavingIntegrations}
                      onClick={handleSaveIntegrations}
                      className="gap-1 text-xs"
                    >
                      {isSavingIntegrations ? (
                        <Loader2 className="size-3.5 animate-spin" />
                      ) : (
                        <CheckCircle2 className="size-3.5" />
                      )}
                      <span>Save Integration Settings</span>
                    </Button>
                  </div>
                </Can>
              </CardContent>
            </Card>
          </TabsContent>
        )}

        {/* 9. SMTP & EMAIL DELIVERY SETTINGS */}
        {canViewSMTP && (
          <TabsContent value="smtp" className="space-y-5 w-full max-w-full min-w-0">
            {/* SMTP Status & Quick Presets Header */}
            <div className="p-4 rounded-xs border border-border bg-card/60 flex flex-col md:flex-row items-start md:items-center justify-between gap-4">
              <div className="space-y-1">
                <div className="flex items-center gap-2">
                  <div className={cn("size-2.5 rounded-full", smtpConfigured ? "bg-emerald-500 animate-pulse" : "bg-amber-500")} />
                  <h3 className="text-sm font-semibold text-foreground">
                    Corporate SMTP Mail Relay Server
                  </h3>
                  <Badge variant={smtpConfigured ? "soft-success" : "outline"} className="text-[10px]">
                    {smtpConfigured ? "Configured & Active" : "Unconfigured / Simulation Mode"}
                  </Badge>
                </div>
                <p className="text-xs text-muted-foreground">
                  Connect your organization&apos;s transactional email provider to send live interview invites, application acknowledgments, and candidate offers.
                </p>
              </div>

              {/* Quick Provider Presets */}
              <div className="flex flex-wrap items-center gap-1.5 self-stretch md:self-auto">
                <span className="text-[10px] text-muted-foreground uppercase font-bold mr-1">
                  Provider Presets:
                </span>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => handleSelectProviderPreset("resend")}
                  className="h-6 text-[11px] px-2 border-border"
                >
                  Resend
                </Button>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => handleSelectProviderPreset("sendgrid")}
                  className="h-6 text-[11px] px-2 border-border"
                >
                  SendGrid
                </Button>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => handleSelectProviderPreset("ses")}
                  className="h-6 text-[11px] px-2 border-border"
                >
                  AWS SES
                </Button>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => handleSelectProviderPreset("gmail")}
                  className="h-6 text-[11px] px-2 border-border"
                >
                  Gmail / Google Workspace
                </Button>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => handleSelectProviderPreset("mailgun")}
                  className="h-6 text-[11px] px-2 border-border"
                >
                  Mailgun
                </Button>
                <Button
                  type="button"
                  size="xs"
                  variant="outline"
                  onClick={() => handleSelectProviderPreset("postmark")}
                  className="h-6 text-[11px] px-2 border-border"
                >
                  Postmark
                </Button>
              </div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-12 gap-5">
              {/* Left Column: Server Connection & Sender Identity (7 cols) */}
              <div className="lg:col-span-7 space-y-5">
                {/* Connection Credentials Card */}
                <Card className="shadow-none border border-border">
                  <CardHeader className="pb-3">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2">
                      <Server className="size-4 text-copper" />
                      <span>SMTP Connection Parameters</span>
                    </CardTitle>
                    <CardDescription className="text-xs">
                      Specify host endpoint, port, and authentication credentials.
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-3.5 text-xs">
                    <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                      <div className="sm:col-span-2 space-y-1">
                        <label className="field-label">SMTP Hostname *</label>
                        <Input
                          placeholder="e.g. smtp.resend.com or smtp.gmail.com"
                          value={smtpHost}
                          onChange={(e) => setSmtpHost(e.target.value)}
                          className="h-8 text-xs font-mono"
                        />
                      </div>
                      <div className="space-y-1">
                        <label className="field-label">Port *</label>
                        <Input
                          placeholder="587"
                          value={smtpPort}
                          onChange={(e) => setSmtpPort(e.target.value)}
                          className="h-8 text-xs font-mono"
                        />
                      </div>
                    </div>

                    <div className="flex items-center gap-4 pt-1">
                      <label className="text-xs font-medium text-foreground">Security Protocol:</label>
                      <div className="flex items-center gap-3 text-xs">
                        <label className="flex items-center gap-1.5 cursor-pointer">
                          <input
                            type="radio"
                            name="smtp_security"
                            checked={!smtpSecure}
                            onChange={() => setSmtpSecure(false)}
                            className="accent-copper size-3.5"
                          />
                          <span>STARTTLS (Port 587 / 25)</span>
                        </label>
                        <label className="flex items-center gap-1.5 cursor-pointer">
                          <input
                            type="radio"
                            name="smtp_security"
                            checked={smtpSecure}
                            onChange={() => setSmtpSecure(true)}
                            className="accent-copper size-3.5"
                          />
                          <span>SSL / TLS (Port 465)</span>
                        </label>
                      </div>
                    </div>

                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 pt-1 border-t border-border/60">
                      <div className="space-y-1">
                        <label className="field-label">SMTP Username / API Key</label>
                        <Input
                          placeholder="resend, apikey, or email"
                          value={smtpUser}
                          onChange={(e) => setSmtpUser(e.target.value)}
                          className="h-8 text-xs font-mono"
                        />
                      </div>
                      <div className="space-y-1">
                        <div className="flex items-center justify-between">
                          <label className="field-label">SMTP Password / Secret</label>
                          <button
                            type="button"
                            onClick={() => setShowSmtpPassword((v) => !v)}
                            className="text-[10px] text-muted-foreground hover:text-foreground flex items-center gap-1"
                          >
                            {showSmtpPassword ? <EyeOff className="size-3" /> : <Eye className="size-3" />}
                            <span>{showSmtpPassword ? "Hide" : "Show"}</span>
                          </button>
                        </div>
                        <Input
                          type={showSmtpPassword ? "text" : "password"}
                          placeholder="••••••••••••••••"
                          value={smtpPass}
                          onChange={(e) => setSmtpPass(e.target.value)}
                          className="h-8 text-xs font-mono"
                        />
                      </div>
                    </div>
                  </CardContent>
                </Card>

                {/* Sender Identity & Mail Routing Card */}
                <Card className="shadow-none border border-border">
                  <CardHeader className="pb-3">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2">
                      <Mail className="size-4 text-copper" />
                      <span>Sender Identity &amp; Corporate Branding</span>
                    </CardTitle>
                    <CardDescription className="text-xs">
                      Configure what candidate recipients see in their inbox &quot;From&quot; header.
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-3.5 text-xs">
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                      <div className="space-y-1">
                        <label className="field-label">Sender Display Name *</label>
                        <Input
                          placeholder="e.g. Karmarat Talent Team"
                          value={smtpFromName}
                          onChange={(e) => setSmtpFromName(e.target.value)}
                          className="h-8 text-xs"
                        />
                      </div>
                      <div className="space-y-1">
                        <label className="field-label">Sender &quot;From&quot; Email Address *</label>
                        <Input
                          type="email"
                          placeholder="talent@karmarat.com"
                          value={smtpFromEmail}
                          onChange={(e) => setSmtpFromEmail(e.target.value)}
                          className="h-8 text-xs"
                        />
                      </div>
                    </div>

                    <div className="space-y-1">
                      <label className="field-label">&quot;Reply-To&quot; Email Address (Optional)</label>
                      <Input
                        type="email"
                        placeholder="recruiting-inbox@karmarat.com"
                        value={smtpReplyTo}
                        onChange={(e) => setSmtpReplyTo(e.target.value)}
                        className="h-8 text-xs"
                      />
                      <span className="text-[10px] text-muted-foreground">
                        Candidate email replies will be directed to this address.
                      </span>
                    </div>

                    <div className="space-y-1">
                      <label className="field-label">Standard Corporate Email Signature &amp; Footer</label>
                      <Textarea
                        rows={3}
                        value={smtpSignature}
                        onChange={(e) => setSmtpSignature(e.target.value)}
                        className="text-xs leading-relaxed font-mono"
                        placeholder="--\nKarmarat Talent Team"
                      />
                    </div>
                  </CardContent>
                </Card>

                {/* Automated Triggers Card */}
                <Card className="shadow-none border border-border">
                  <CardHeader className="pb-3">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2">
                      <Sparkles className="size-4 text-copper" />
                      <span>Automated Recruitment Email Triggers</span>
                    </CardTitle>
                    <CardDescription className="text-xs">
                      Enable system actions that trigger real-time candidate notifications.
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-2.5 text-xs">
                    <label className="flex items-start gap-2.5 p-2 rounded-xs border border-border bg-card hover:bg-muted/30 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={smtpAutoAppConfirm}
                        onChange={(e) => setSmtpAutoAppConfirm(e.target.checked)}
                        className="accent-copper size-4 mt-0.5"
                      />
                      <div>
                        <span className="font-semibold text-foreground block">
                          Instant Application Acknowledgment
                        </span>
                        <span className="text-[11px] text-muted-foreground">
                          Auto-dispatch confirmation email with tracking link when candidate applies via Careers Portal.
                        </span>
                      </div>
                    </label>

                    <label className="flex items-start gap-2.5 p-2 rounded-xs border border-border bg-card hover:bg-muted/30 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={smtpAutoInterviewInvite}
                        onChange={(e) => setSmtpAutoInterviewInvite(e.target.checked)}
                        className="accent-copper size-4 mt-0.5"
                      />
                      <div>
                        <span className="font-semibold text-foreground block">
                          Interview Schedule &amp; Video Link Dispatch
                        </span>
                        <span className="text-[11px] text-muted-foreground">
                          Auto-send calendar briefing and meeting link whenever an interview round is booked.
                        </span>
                      </div>
                    </label>

                    <label className="flex items-start gap-2.5 p-2 rounded-xs border border-border bg-card hover:bg-muted/30 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={smtpAutoOfferNotice}
                        onChange={(e) => setSmtpAutoOfferNotice(e.target.checked)}
                        className="accent-copper size-4 mt-0.5"
                      />
                      <div>
                        <span className="font-semibold text-foreground block">
                          Offer Package Notification
                        </span>
                        <span className="text-[11px] text-muted-foreground">
                          Notify candidate immediately when a formal compensation package is generated.
                        </span>
                      </div>
                    </label>
                  </CardContent>
                </Card>

                {/* Save Settings Action Bar */}
                <div className="flex items-center justify-end gap-2 pt-2">
                  <Button
                    size="sm"
                    variant="accent"
                    disabled={smtpSaving}
                    onClick={handleSaveSmtp}
                    className="gap-1.5 text-xs font-semibold px-4"
                  >
                    {smtpSaving ? <Loader2 className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />}
                    <span>Save SMTP Settings</span>
                  </Button>
                </div>
              </div>

              {/* Right Column: Connection Diagnostics & Live Test (5 cols) */}
              <div className="lg:col-span-5 space-y-5">
                <Card className="shadow-none border border-border">
                  <CardHeader className="pb-3">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2">
                      <KeyRound className="size-4 text-copper" />
                      <span>SMTP Handshake &amp; Live Test</span>
                    </CardTitle>
                    <CardDescription className="text-xs">
                      Verify socket connection, TLS certificate, and send a test message.
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-3.5 text-xs">
                    <div className="space-y-1">
                      <label className="field-label">Recipient Test Email</label>
                      <Input
                        type="email"
                        placeholder="your-email@company.com"
                        value={smtpTestEmail}
                        onChange={(e) => setSmtpTestEmail(e.target.value)}
                        className="h-8 text-xs"
                      />
                    </div>

                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      disabled={smtpTesting}
                      onClick={handleTestSmtp}
                      className="w-full gap-1.5 text-xs font-medium border-copper/40 hover:bg-copper/10 hover:text-copper"
                    >
                      {smtpTesting ? (
                        <>
                          <Loader2 className="size-3.5 animate-spin text-copper" />
                          <span>Testing Handshake &amp; Dispatching...</span>
                        </>
                      ) : (
                        <>
                          <Send className="size-3.5 text-copper" />
                          <span>Test Connection &amp; Send Verification Email</span>
                        </>
                      )}
                    </Button>

                    {/* Test Result Indicator */}
                    {smtpLastTestedAt && (
                      <div className={cn(
                        "p-3 rounded-xs border text-xs space-y-1",
                        smtpLastTestStatus === "success"
                          ? "bg-emerald-500/10 border-emerald-500/30 text-emerald-600 dark:text-emerald-400"
                          : "bg-destructive/10 border-destructive/30 text-destructive"
                      )}>
                        <div className="flex items-center gap-1.5 font-semibold">
                          {smtpLastTestStatus === "success" ? (
                            <CheckCircle2 className="size-3.5 shrink-0" />
                          ) : (
                            <ShieldAlert className="size-3.5 shrink-0" />
                          )}
                          <span>
                            {smtpLastTestStatus === "success" ? "Handshake Verified" : "Verification Failed"}
                          </span>
                        </div>
                        <p className="text-[11px] leading-relaxed">
                          {smtpLastTestMessage}
                        </p>
                        <span className="text-[10px] text-muted-foreground block pt-1">
                          Tested at: {new Date(smtpLastTestedAt).toLocaleString()}
                        </span>
                      </div>
                    )}

                    {/* Diagnostic Output Console */}
                    <div className="space-y-1 pt-1">
                      <div className="flex items-center justify-between text-[10px] uppercase font-bold text-muted-foreground">
                        <span>Connection Log Console</span>
                        {smtpLogs.length > 0 && (
                          <button
                            type="button"
                            onClick={() => setSmtpLogs([])}
                            className="text-copper hover:underline lowercase font-mono"
                          >
                            clear
                          </button>
                        )}
                      </div>
                      <div className="p-3 bg-zinc-950 text-zinc-200 rounded-xs border border-border text-[11px] font-mono leading-relaxed min-h-[140px] max-h-[220px] overflow-y-auto space-y-1">
                        {smtpLogs.length === 0 ? (
                          <span className="text-zinc-500 italic">
                            Click &quot;Test Connection&quot; above to inspect live SMTP handshake logs...
                          </span>
                        ) : (
                          smtpLogs.map((lg, i) => (
                            <div key={i} className="whitespace-pre-wrap break-all">
                              {lg}
                            </div>
                          ))
                        )}
                      </div>
                    </div>
                  </CardContent>
                </Card>
              </div>
            </div>
          </TabsContent>
        )}
      </Tabs>

      {/* CREATE CUSTOM ROLE MODAL */}
      <Dialog open={createRoleModalOpen} onOpenChange={setCreateRoleModalOpen}>
        <DialogContent className="sm:max-w-2xl max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold flex items-center gap-2">
              <ShieldCheck className="size-4 text-copper" />
              <span>Create Custom Dynamic Role</span>
            </DialogTitle>
            <div className="text-xs text-muted-foreground">
              Define a new role and choose granular system permissions.
            </div>
          </DialogHeader>

          <div className="space-y-4 py-2 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="space-y-1">
                <label className="field-label">Role Title *</label>
                <Input
                  value={roleName}
                  onChange={(e) => {
                    setRoleName(e.target.value);
                    if (!roleSlug) {
                      setRoleSlug(
                        e.target.value
                          .toLowerCase()
                          .replace(/[^a-z0-9]+/g, "_")
                          .replace(/^_+|_+$/g, ""),
                      );
                    }
                  }}
                  placeholder="e.g. Lead Technical Recruiter"
                  className="h-8 text-xs"
                  required
                />
              </div>

              <div className="space-y-1">
                <label className="field-label">Role Identifier Slug</label>
                <Input
                  value={roleSlug}
                  onChange={(e) => setRoleSlug(e.target.value)}
                  placeholder="e.g. lead_tech_recruiter"
                  className="h-8 text-xs"
                />
              </div>

              <div className="space-y-1 sm:col-span-2">
                <label className="field-label">Role Description</label>
                <Textarea
                  rows={2}
                  value={roleDesc}
                  onChange={(e) => setRoleDesc(e.target.value)}
                  placeholder="Responsibilities and access scope for this role..."
                  className="text-xs"
                />
              </div>

              <div className="space-y-1">
                <label className="field-label">Badge Label</label>
                <Input
                  value={roleBadge}
                  onChange={(e) => setRoleBadge(e.target.value)}
                  placeholder="e.g. Recruiter Lead"
                  className="h-8 text-xs"
                />
              </div>
            </div>

            {/* Permission matrix generated from the platform's catalogue */}
            <PermissionPicker
              catalogue={catalogue}
              selected={rolePerms}
              onChange={setRolePerms}
              delegatable={delegatablePermissions}
            />
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setCreateRoleModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingRole}
              onClick={handleCreateRole}
              className="gap-1"
            >
              {isCreatingRole ? <Loader2 className="size-3 animate-spin" /> : <Plus className="size-3" />}
              <span>Save &amp; Create Role</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* EDIT ROLE & PERMISSIONS MODAL */}
      <Dialog open={!!editingRole} onOpenChange={(open) => !open && setEditingRole(null)}>
        <DialogContent className="sm:max-w-2xl max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold flex items-center gap-2">
              <Edit2 className="size-4 text-copper" />
              <span>Edit Role &amp; Permissions: {editingRole?.name}</span>
            </DialogTitle>
            <div className="text-xs text-muted-foreground">
              Update role attributes and toggle active permissions in database.
            </div>
          </DialogHeader>

          {editingRole && (
            <div className="space-y-4 py-2 text-xs">
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <div className="space-y-1">
                  <label className="field-label">Role Title</label>
                  <Input
                    value={editRoleName}
                    onChange={(e) => setEditRoleName(e.target.value)}
                    className="h-8 text-xs"
                  />
                </div>

                <div className="space-y-1">
                  <label className="field-label">Badge Label</label>
                  <Input
                    value={editRoleBadge}
                    onChange={(e) => setEditRoleBadge(e.target.value)}
                    className="h-8 text-xs"
                  />
                </div>

                <div className="space-y-1 sm:col-span-2">
                  <label className="field-label">Role Description</label>
                  <Textarea
                    rows={2}
                    value={editRoleDesc}
                    onChange={(e) => setEditRoleDesc(e.target.value)}
                    className="text-xs"
                  />
                </div>
              </div>

              {/* Permission matrix generated from the platform's catalogue */}
              <PermissionPicker
                catalogue={catalogue}
                selected={editRolePerms}
                onChange={setEditRolePerms}
                delegatable={delegatablePermissions}
              />
            </div>
          )}

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingRole(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingRole}
              onClick={handleSaveEditRole}
              className="gap-1"
            >
              {isUpdatingRole ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Role Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add User Modal */}
      <Dialog open={userModalOpen} onOpenChange={setUserModalOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add New User Account</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Full Name *</label>
              <Input
                value={newUserName}
                onChange={(e) => setNewUserName(e.target.value)}
                placeholder="e.g. Alex Morgan"
                className="h-8 text-xs"
              />
            </div>

            <div className="space-y-1">
              <label className="field-label">Email Address *</label>
              <Input
                type="email"
                value={newUserEmail}
                onChange={(e) => setNewUserEmail(e.target.value)}
                placeholder="e.g. alex@example.com"
                className="h-8 text-xs"
              />
            </div>

            <div className="space-y-1">
              <label className="field-label">Temporary Password *</label>
              <Input
                type="password"
                value={newUserPassword}
                onChange={(e) => setNewUserPassword(e.target.value)}
                className="h-8 text-xs"
              />
            </div>

            <div className="space-y-1">
              <label className="field-label">Assigned Roles *</label>
              <div className="max-h-48 overflow-y-auto rounded-xs border border-border bg-card divide-y divide-border">
                {assignableRoles.length === 0 ? (
                  <div className="p-2.5 text-[11px] text-muted-foreground">
                    No roles are available for you to assign.
                  </div>
                ) : (
                  assignableRoles.map((r) => (
                    <label
                      key={r.id}
                      className="flex items-start gap-2 p-2 hover:bg-muted/40 cursor-pointer"
                    >
                      <input
                        type="checkbox"
                        checked={newUserRoleIds.includes(r.id)}
                        onChange={(e) =>
                          setNewUserRoleIds((prev) =>
                            e.target.checked
                              ? [...prev, r.id]
                              : prev.filter((id) => id !== r.id),
                          )
                        }
                        className="mt-0.5 size-3.5 rounded-xs accent-copper cursor-pointer"
                      />
                      <div className="min-w-0">
                        <span className="font-medium text-foreground block text-[11px] leading-tight">
                          {r.name}{" "}
                          <span className="text-muted-foreground font-normal">
                            {r.isSuperAdmin ? "(Super Admin)" : r.isSystem ? "(Built-in)" : "(Custom)"}
                          </span>
                        </span>
                        <span className="text-[10px] text-muted-foreground leading-tight block">
                          {r.permissions.length} permissions
                        </span>
                      </div>
                    </label>
                  ))
                )}
              </div>
              <p className="text-[10px] text-muted-foreground">
                The user&apos;s access is the union of every role selected. The first role becomes
                their primary title.
              </p>
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setUserModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingUser}
              onClick={handleCreateUser}
              className="gap-1"
            >
              {isCreatingUser ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Create Account</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Assign Roles Modal */}
      <Dialog open={!!assigningUser} onOpenChange={(open) => !open && setAssigningUser(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">
              Assign Roles — {assigningUser?.name}
            </DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <p className="text-[11px] text-muted-foreground">
              A user receives the combined permissions of every role assigned to them. You can only
              assign roles whose permissions you hold yourself.
            </p>

            <div className="max-h-64 overflow-y-auto rounded-xs border border-border bg-card divide-y divide-border">
              {assignableRoles.length === 0 ? (
                <div className="p-2.5 text-[11px] text-muted-foreground">
                  No roles are available for you to assign.
                </div>
              ) : (
                assignableRoles.map((r) => {
                  const checked = assignRoleIds.includes(r.id);
                  return (
                    <div key={r.id} className="flex items-start gap-2 p-2 hover:bg-muted/40">
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={(e) =>
                          setAssignRoleIds((prev) => {
                            const next = e.target.checked
                              ? [...prev, r.id]
                              : prev.filter((id) => id !== r.id);
                            if (!next.includes(assignPrimaryRoleId)) {
                              setAssignPrimaryRoleId(next[0] ?? "");
                            }
                            return next;
                          })
                        }
                        className="mt-0.5 size-3.5 rounded-xs accent-copper cursor-pointer"
                      />
                      <div className="min-w-0 flex-1">
                        <span className="font-medium text-foreground block text-[11px] leading-tight">
                          {r.name}{" "}
                          <span className="text-muted-foreground font-normal">
                            {r.isSuperAdmin ? "(Super Admin)" : r.isSystem ? "(Built-in)" : "(Custom)"}
                          </span>
                        </span>
                        <span className="text-[10px] text-muted-foreground leading-tight block">
                          {r.permissions.length} permissions
                        </span>
                      </div>
                      {checked && (
                        <button
                          type="button"
                          onClick={() => setAssignPrimaryRoleId(r.id)}
                          className={`shrink-0 text-[10px] px-1.5 py-0.5 rounded-xs border transition-colors ${
                            assignPrimaryRoleId === r.id
                              ? "border-copper/40 bg-copper/10 text-copper"
                              : "border-border text-muted-foreground hover:text-foreground"
                          }`}
                          title="Use as the user's primary role title"
                        >
                          {assignPrimaryRoleId === r.id ? "Primary" : "Set primary"}
                        </button>
                      )}
                    </div>
                  );
                })
              )}
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setAssigningUser(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isAssigningRoles}
              onClick={handleSaveAssignedRoles}
              className="gap-1"
            >
              {isAssigningRoles ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Role Assignment</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Department Modal */}
      <Dialog open={deptModalOpen} onOpenChange={setDeptModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Department</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Department Name</label>
              <Input
                value={newDeptName}
                onChange={(e) => setNewDeptName(e.target.value)}
                placeholder="e.g. Data Science & AI"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Department Code</label>
              <Input
                value={newDeptCode}
                onChange={(e) => setNewDeptCode(e.target.value)}
                placeholder="e.g. DSAI"
                className="h-8 text-xs uppercase"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setDeptModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingDept}
              onClick={handleCreateDept}
              className="gap-1"
            >
              {isCreatingDept ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Add Department</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Location Modal */}
      <Dialog open={locModalOpen} onOpenChange={setLocModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Office Location</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Location Title</label>
              <Input
                value={newLocName}
                onChange={(e) => setNewLocName(e.target.value)}
                placeholder="e.g. Seattle Innovation Hub"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">City</label>
              <Input
                value={newLocCity}
                onChange={(e) => setNewLocCity(e.target.value)}
                placeholder="e.g. Seattle, WA"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Country</label>
              <Input
                value={newLocCountry}
                onChange={(e) => setNewLocCountry(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setLocModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingLoc}
              onClick={handleCreateLoc}
              className="gap-1"
            >
              {isCreatingLoc ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Add Location</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Work Mode Modal */}
      <Dialog open={workModeModalOpen} onOpenChange={setWorkModeModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Work Mode</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Work Mode Name *</label>
              <Input
                value={newWorkModeName}
                onChange={(e) => {
                  setNewWorkModeName(e.target.value);
                  if (!newWorkModeSlug) {
                    setNewWorkModeSlug(
                      e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "")
                    );
                  }
                }}
                placeholder="e.g. Hybrid (3 Days Office)"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={newWorkModeSlug}
                onChange={(e) => setNewWorkModeSlug(e.target.value)}
                placeholder="e.g. hybrid_3days"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={newWorkModeDesc}
                onChange={(e) => setNewWorkModeDesc(e.target.value)}
                placeholder="e.g. Tuesday-Thursday in office, Monday/Friday remote"
                className="h-8 text-xs"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setWorkModeModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingWorkMode}
              onClick={handleCreateWorkMode}
              className="gap-1"
            >
              {isCreatingWorkMode ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Add Work Mode</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Employment Type Modal */}
      <Dialog open={empTypeModalOpen} onOpenChange={setEmpTypeModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Employment Type</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Employment Type Name *</label>
              <Input
                value={newEmpTypeName}
                onChange={(e) => {
                  setNewEmpTypeName(e.target.value);
                  if (!newEmpTypeSlug) {
                    setNewEmpTypeSlug(
                      e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "")
                    );
                  }
                }}
                placeholder="e.g. Fixed-Term Contract (12 Mo)"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={newEmpTypeSlug}
                onChange={(e) => setNewEmpTypeSlug(e.target.value)}
                placeholder="e.g. contract_12mo"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={newEmpTypeDesc}
                onChange={(e) => setNewEmpTypeDesc(e.target.value)}
                placeholder="e.g. 12-month fixed term with renewal evaluation"
                className="h-8 text-xs"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEmpTypeModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingEmpType}
              onClick={handleCreateEmpType}
              className="gap-1"
            >
              {isCreatingEmpType ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Add Employment Type</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Department Modal */}
      <Dialog open={!!editingDept} onOpenChange={(open) => !open && setEditingDept(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Department</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Department Name *</label>
              <Input
                value={editDeptName}
                onChange={(e) => setEditDeptName(e.target.value)}
                placeholder="e.g. Engineering"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Code / Abbreviation *</label>
              <Input
                value={editDeptCode}
                onChange={(e) => setEditDeptCode(e.target.value.toUpperCase())}
                placeholder="e.g. ENG"
                className="h-8 text-xs"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingDept(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingDept}
              onClick={handleSaveEditDept}
              className="gap-1"
            >
              {isUpdatingDept ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Location Modal */}
      <Dialog open={!!editingLoc} onOpenChange={(open) => !open && setEditingLoc(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Office Location</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Location Name / Hub *</label>
              <Input
                value={editLocName}
                onChange={(e) => setEditLocName(e.target.value)}
                placeholder="e.g. London EMEA Office"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">City *</label>
              <Input
                value={editLocCity}
                onChange={(e) => setEditLocCity(e.target.value)}
                placeholder="e.g. London"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Country *</label>
              <Input
                value={editLocCountry}
                onChange={(e) => setEditLocCountry(e.target.value)}
                placeholder="e.g. United Kingdom"
                className="h-8 text-xs"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingLoc(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingLoc}
              onClick={handleSaveEditLoc}
              className="gap-1"
            >
              {isUpdatingLoc ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Work Mode Modal */}
      <Dialog open={!!editingWorkMode} onOpenChange={(open) => !open && setEditingWorkMode(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Work Mode</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Work Mode Name *</label>
              <Input
                value={editWorkModeName}
                onChange={(e) => setEditWorkModeName(e.target.value)}
                placeholder="e.g. Hybrid (3 Days Office)"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={editWorkModeSlug}
                onChange={(e) => setEditWorkModeSlug(e.target.value)}
                placeholder="e.g. hybrid_3days"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={editWorkModeDesc}
                onChange={(e) => setEditWorkModeDesc(e.target.value)}
                placeholder="e.g. Tuesday-Thursday in office"
                className="h-8 text-xs"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingWorkMode(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingWorkMode}
              onClick={handleSaveEditWorkMode}
              className="gap-1"
            >
              {isUpdatingWorkMode ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Employment Type Modal */}
      <Dialog open={!!editingEmpType} onOpenChange={(open) => !open && setEditingEmpType(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Employment Type</DialogTitle>
          </DialogHeader>

          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Employment Type Name *</label>
              <Input
                value={editEmpTypeName}
                onChange={(e) => setEditEmpTypeName(e.target.value)}
                placeholder="e.g. Fixed-Term Contract"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={editEmpTypeSlug}
                onChange={(e) => setEditEmpTypeSlug(e.target.value)}
                placeholder="e.g. contract_12mo"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={editEmpTypeDesc}
                onChange={(e) => setEditEmpTypeDesc(e.target.value)}
                placeholder="e.g. 12-month fixed term"
                className="h-8 text-xs"
              />
            </div>
          </div>

          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingEmpType(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingEmpType}
              onClick={handleSaveEditEmpType}
              className="gap-1"
            >
              {isUpdatingEmpType ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ADD EXPERIENCE LEVEL MODAL */}
      <Dialog open={expLevelModalOpen} onOpenChange={setExpLevelModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Experience Level</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Experience Level Name *</label>
              <Input
                value={newExpLevelName}
                onChange={(e) => {
                  setNewExpLevelName(e.target.value);
                  if (!newExpLevelSlug) {
                    setNewExpLevelSlug(
                      e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "")
                    );
                  }
                }}
                placeholder="e.g. Senior Staff Engineer"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={newExpLevelSlug}
                onChange={(e) => setNewExpLevelSlug(e.target.value)}
                placeholder="e.g. senior_staff"
                className="h-8 text-xs"
              />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <label className="field-label">Min Experience (Yrs)</label>
                <Input
                  type="number"
                  min="0"
                  max="50"
                  value={newExpLevelMinYears}
                  onChange={(e) => setNewExpLevelMinYears(Number(e.target.value))}
                  className="h-8 text-xs"
                />
              </div>
              <div className="space-y-1">
                <label className="field-label">Max Experience (Yrs)</label>
                <Input
                  type="number"
                  min="0"
                  max="50"
                  value={newExpLevelMaxYears}
                  onChange={(e) => setNewExpLevelMaxYears(Number(e.target.value))}
                  className="h-8 text-xs"
                />
              </div>
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={newExpLevelDesc}
                onChange={(e) => setNewExpLevelDesc(e.target.value)}
                placeholder="e.g. Principal organizational leadership"
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setExpLevelModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingExpLevel}
              onClick={handleCreateExpLevel}
              className="gap-1"
            >
              {isCreatingExpLevel ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save &amp; Create</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* EDIT EXPERIENCE LEVEL MODAL */}
      <Dialog open={!!editingExpLevel} onOpenChange={(open) => !open && setEditingExpLevel(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Experience Level</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Experience Level Name *</label>
              <Input
                value={editExpLevelName}
                onChange={(e) => setEditExpLevelName(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={editExpLevelSlug}
                onChange={(e) => setEditExpLevelSlug(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <label className="field-label">Min Experience (Yrs)</label>
                <Input
                  type="number"
                  min="0"
                  max="50"
                  value={editExpLevelMinYears}
                  onChange={(e) => setEditExpLevelMinYears(Number(e.target.value))}
                  className="h-8 text-xs"
                />
              </div>
              <div className="space-y-1">
                <label className="field-label">Max Experience (Yrs)</label>
                <Input
                  type="number"
                  min="0"
                  max="50"
                  value={editExpLevelMaxYears}
                  onChange={(e) => setEditExpLevelMaxYears(Number(e.target.value))}
                  className="h-8 text-xs"
                />
              </div>
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={editExpLevelDesc}
                onChange={(e) => setEditExpLevelDesc(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingExpLevel(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingExpLevel}
              onClick={handleSaveEditExpLevel}
              className="gap-1"
            >
              {isUpdatingExpLevel ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ADD EDUCATION LEVEL MODAL */}
      <Dialog open={eduLevelModalOpen} onOpenChange={setEduLevelModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Education Requirement</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Education Requirement Name *</label>
              <Input
                value={newEduLevelName}
                onChange={(e) => {
                  setNewEduLevelName(e.target.value);
                  if (!newEduLevelSlug) {
                    setNewEduLevelSlug(
                      e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "")
                    );
                  }
                }}
                placeholder="e.g. Associate Degree"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={newEduLevelSlug}
                onChange={(e) => setNewEduLevelSlug(e.target.value)}
                placeholder="e.g. associate_degree"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={newEduLevelDesc}
                onChange={(e) => setNewEduLevelDesc(e.target.value)}
                placeholder="e.g. 2-year postsecondary degree"
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEduLevelModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingEduLevel}
              onClick={handleCreateEduLevel}
              className="gap-1"
            >
              {isCreatingEduLevel ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save &amp; Create</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* EDIT EDUCATION LEVEL MODAL */}
      <Dialog open={!!editingEduLevel} onOpenChange={(open) => !open && setEditingEduLevel(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Education Requirement</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Education Requirement Name *</label>
              <Input
                value={editEduLevelName}
                onChange={(e) => setEditEduLevelName(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={editEduLevelSlug}
                onChange={(e) => setEditEduLevelSlug(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={editEduLevelDesc}
                onChange={(e) => setEditEduLevelDesc(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingEduLevel(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingEduLevel}
              onClick={handleSaveEditEduLevel}
              className="gap-1"
            >
              {isUpdatingEduLevel ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* --------------------------------------------------------------------- */}
      {/* 1. ADD CURRENCY MODAL */}
      {/* --------------------------------------------------------------------- */}
      <Dialog open={currencyModalOpen} onOpenChange={setCurrencyModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Currency Master</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <label className="field-label">Currency Code *</label>
                <Input
                  value={newCurrCode}
                  onChange={(e) => {
                    setNewCurrCode(e.target.value);
                    if (!newCurrName) setNewCurrName(`${e.target.value.toUpperCase()} (${newCurrSymbol || "$"})`);
                  }}
                  placeholder="e.g. JPY"
                  className="h-8 text-xs uppercase"
                />
              </div>
              <div className="space-y-1">
                <label className="field-label">Symbol *</label>
                <Input
                  value={newCurrSymbol}
                  onChange={(e) => {
                    setNewCurrSymbol(e.target.value);
                    if (newCurrCode) setNewCurrName(`${newCurrCode.toUpperCase()} (${e.target.value})`);
                  }}
                  placeholder="e.g. ¥"
                  className="h-8 text-xs"
                />
              </div>
            </div>
            <div className="space-y-1">
              <label className="field-label">Display Name *</label>
              <Input
                value={newCurrName}
                onChange={(e) => setNewCurrName(e.target.value)}
                placeholder="e.g. Japanese Yen (¥)"
                className="h-8 text-xs"
              />
            </div>
            <div className="flex items-center gap-2 pt-1">
              <input
                type="checkbox"
                id="newCurrDefaultCheck"
                checked={newCurrDefault}
                onChange={(e) => setNewCurrDefault(e.target.checked)}
                className="rounded text-copper focus:ring-copper"
              />
              <label htmlFor="newCurrDefaultCheck" className="text-xs text-foreground cursor-pointer">
                Set as Default Base Currency
              </label>
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setCurrencyModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingCurrency}
              onClick={handleCreateCurrency}
              className="gap-1"
            >
              {isCreatingCurrency ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save &amp; Create</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* EDIT CURRENCY MODAL */}
      <Dialog open={!!editingCurrency} onOpenChange={(open) => !open && setEditingCurrency(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Currency Master</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="grid grid-cols-2 gap-2">
              <div className="space-y-1">
                <label className="field-label">Currency Code *</label>
                <Input
                  value={editCurrCode}
                  onChange={(e) => setEditCurrCode(e.target.value)}
                  placeholder="e.g. USD"
                  className="h-8 text-xs uppercase"
                />
              </div>
              <div className="space-y-1">
                <label className="field-label">Symbol *</label>
                <Input
                  value={editCurrSymbol}
                  onChange={(e) => setEditCurrSymbol(e.target.value)}
                  placeholder="e.g. $"
                  className="h-8 text-xs"
                />
              </div>
            </div>
            <div className="space-y-1">
              <label className="field-label">Display Name *</label>
              <Input
                value={editCurrName}
                onChange={(e) => setEditCurrName(e.target.value)}
                placeholder="e.g. US Dollar ($)"
                className="h-8 text-xs"
              />
            </div>
            <div className="flex items-center gap-2 pt-1">
              <input
                type="checkbox"
                id="editCurrDefaultCheck"
                checked={editCurrDefault}
                onChange={(e) => setEditCurrDefault(e.target.checked)}
                className="rounded text-copper focus:ring-copper"
              />
              <label htmlFor="editCurrDefaultCheck" className="text-xs text-foreground cursor-pointer">
                Set as Default Base Currency
              </label>
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingCurrency(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingCurrency}
              onClick={handleSaveEditCurrency}
              className="gap-1"
            >
              {isUpdatingCurrency ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* --------------------------------------------------------------------- */}
      {/* 2. ADD PAY FREQUENCY MODAL */}
      {/* --------------------------------------------------------------------- */}
      <Dialog open={payFreqModalOpen} onOpenChange={setPayFreqModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Pay Frequency</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Frequency Name *</label>
              <Input
                value={newPayFreqName}
                onChange={(e) => {
                  setNewPayFreqName(e.target.value);
                  if (!newPayFreqSlug) {
                    setNewPayFreqSlug(
                      e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "")
                    );
                  }
                }}
                placeholder="e.g. Bi-Weekly Pay"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={newPayFreqSlug}
                onChange={(e) => setNewPayFreqSlug(e.target.value)}
                placeholder="e.g. bi_weekly"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={newPayFreqDesc}
                onChange={(e) => setNewPayFreqDesc(e.target.value)}
                placeholder="e.g. Disbursed 26 times per year"
                className="h-8 text-xs"
              />
            </div>
            <div className="flex items-center gap-2 pt-1">
              <input
                type="checkbox"
                id="newPayFreqDefaultCheck"
                checked={newPayFreqDefault}
                onChange={(e) => setNewPayFreqDefault(e.target.checked)}
                className="rounded text-copper focus:ring-copper"
              />
              <label htmlFor="newPayFreqDefaultCheck" className="text-xs text-foreground cursor-pointer">
                Set as Default Frequency
              </label>
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setPayFreqModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingPayFreq}
              onClick={handleCreatePayFreq}
              className="gap-1"
            >
              {isCreatingPayFreq ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save &amp; Create</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* EDIT PAY FREQUENCY MODAL */}
      <Dialog open={!!editingPayFreq} onOpenChange={(open) => !open && setEditingPayFreq(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Pay Frequency</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Frequency Name *</label>
              <Input
                value={editPayFreqName}
                onChange={(e) => setEditPayFreqName(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={editPayFreqSlug}
                onChange={(e) => setEditPayFreqSlug(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={editPayFreqDesc}
                onChange={(e) => setEditPayFreqDesc(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="flex items-center gap-2 pt-1">
              <input
                type="checkbox"
                id="editPayFreqDefaultCheck"
                checked={editPayFreqDefault}
                onChange={(e) => setEditPayFreqDefault(e.target.checked)}
                className="rounded text-copper focus:ring-copper"
              />
              <label htmlFor="editPayFreqDefaultCheck" className="text-xs text-foreground cursor-pointer">
                Set as Default Frequency
              </label>
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingPayFreq(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingPayFreq}
              onClick={handleSaveEditPayFreq}
              className="gap-1"
            >
              {isUpdatingPayFreq ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* --------------------------------------------------------------------- */}
      {/* 3. ADD REQUISITION STATUS MODAL */}
      {/* --------------------------------------------------------------------- */}
      <Dialog open={jobStatusModalOpen} onOpenChange={setJobStatusModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Requisition Status</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Status Name *</label>
              <Input
                value={newStatusName}
                onChange={(e) => {
                  setNewStatusName(e.target.value);
                  if (!newStatusSlug) {
                    setNewStatusSlug(
                      e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "")
                    );
                  }
                }}
                placeholder="e.g. Sourcing Phase"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={newStatusSlug}
                onChange={(e) => setNewStatusSlug(e.target.value)}
                placeholder="e.g. sourcing"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Badge Color Variant</label>
              <select
                value={newStatusBadge}
                onChange={(e) => setNewStatusBadge(e.target.value)}
                className="h-8 w-full rounded-xs border border-border bg-card px-2 text-xs text-foreground"
              >
                <option value="soft-success">Green (Active / Open / Live)</option>
                <option value="secondary">Gray (Draft / Preparation)</option>
                <option value="warning">Amber (On Hold / Paused)</option>
                <option value="destructive">Red (Closed / Cancelled)</option>
              </select>
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={newStatusDesc}
                onChange={(e) => setNewStatusDesc(e.target.value)}
                placeholder="e.g. Active outbound candidate search"
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setJobStatusModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingJobStatus}
              onClick={handleCreateJobStatus}
              className="gap-1"
            >
              {isCreatingJobStatus ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save &amp; Create</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* EDIT REQUISITION STATUS MODAL */}
      <Dialog open={!!editingJobStatus} onOpenChange={(open) => !open && setEditingJobStatus(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Requisition Status</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Status Name *</label>
              <Input
                value={editStatusName}
                onChange={(e) => setEditStatusName(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={editStatusSlug}
                onChange={(e) => setEditStatusSlug(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Badge Color Variant</label>
              <select
                value={editStatusBadge}
                onChange={(e) => setEditStatusBadge(e.target.value)}
                className="h-8 w-full rounded-xs border border-border bg-card px-2 text-xs text-foreground"
              >
                <option value="soft-success">Green (Active / Open / Live)</option>
                <option value="secondary">Gray (Draft / Preparation)</option>
                <option value="warning">Amber (On Hold / Paused)</option>
                <option value="destructive">Red (Closed / Cancelled)</option>
              </select>
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={editStatusDesc}
                onChange={(e) => setEditStatusDesc(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingJobStatus(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingJobStatus}
              onClick={handleSaveEditJobStatus}
              className="gap-1"
            >
              {isUpdatingJobStatus ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* --------------------------------------------------------------------- */}
      {/* 4. ADD INTERVIEW TYPE MODAL */}
      {/* --------------------------------------------------------------------- */}
      <Dialog open={interviewTypeModalOpen} onOpenChange={setInterviewTypeModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Interview Round Type</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Round Type Name *</label>
              <Input
                value={newITypeName}
                onChange={(e) => {
                  setNewITypeName(e.target.value);
                  if (!newITypeSlug) {
                    setNewITypeSlug(
                      e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "")
                    );
                  }
                }}
                placeholder="e.g. Executive Leadership"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={newITypeSlug}
                onChange={(e) => setNewITypeSlug(e.target.value)}
                placeholder="e.g. executive"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Default Duration (Minutes)</label>
              <select
                value={newITypeDuration}
                onChange={(e) => setNewITypeDuration(Number(e.target.value))}
                className="h-8 w-full rounded-xs border border-border bg-card px-2 text-xs text-foreground"
              >
                <option value={30}>30 Minutes</option>
                <option value={45}>45 Minutes</option>
                <option value={60}>60 Minutes</option>
                <option value={90}>90 Minutes</option>
                <option value={120}>120 Minutes</option>
              </select>
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={newITypeDesc}
                onChange={(e) => setNewITypeDesc(e.target.value)}
                placeholder="e.g. Core values, cultural alignment and final review"
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setInterviewTypeModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingInterviewType}
              onClick={handleCreateInterviewType}
              className="gap-1"
            >
              {isCreatingInterviewType ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save &amp; Create</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* EDIT INTERVIEW TYPE MODAL */}
      <Dialog open={!!editingInterviewType} onOpenChange={(open) => !open && setEditingInterviewType(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Interview Round Type</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Round Type Name *</label>
              <Input
                value={editITypeName}
                onChange={(e) => setEditITypeName(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={editITypeSlug}
                onChange={(e) => setEditITypeSlug(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Default Duration (Minutes)</label>
              <select
                value={editITypeDuration}
                onChange={(e) => setEditITypeDuration(Number(e.target.value))}
                className="h-8 w-full rounded-xs border border-border bg-card px-2 text-xs text-foreground"
              >
                <option value={30}>30 Minutes</option>
                <option value={45}>45 Minutes</option>
                <option value={60}>60 Minutes</option>
                <option value={90}>90 Minutes</option>
                <option value={120}>120 Minutes</option>
              </select>
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={editITypeDesc}
                onChange={(e) => setEditITypeDesc(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingInterviewType(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingInterviewType}
              onClick={handleSaveEditInterviewType}
              className="gap-1"
            >
              {isUpdatingInterviewType ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* --------------------------------------------------------------------- */}
      {/* 5. ADD BENEFIT CATEGORY MODAL */}
      {/* --------------------------------------------------------------------- */}
      <Dialog open={benefitCatModalOpen} onOpenChange={setBenefitCatModalOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Add Benefit Category</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Category Name *</label>
              <Input
                value={newBCatName}
                onChange={(e) => {
                  setNewBCatName(e.target.value);
                  if (!newBCatSlug) {
                    setNewBCatSlug(
                      e.target.value.toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "")
                    );
                  }
                }}
                placeholder="e.g. Parental Support"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={newBCatSlug}
                onChange={(e) => setNewBCatSlug(e.target.value)}
                placeholder="e.g. parental"
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={newBCatDesc}
                onChange={(e) => setNewBCatDesc(e.target.value)}
                placeholder="e.g. Family planning, childcare subsidies and leave"
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setBenefitCatModalOpen(false)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isCreatingBenefitCat}
              onClick={handleCreateBenefitCat}
              className="gap-1"
            >
              {isCreatingBenefitCat ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save &amp; Create</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* EDIT BENEFIT CATEGORY MODAL */}
      <Dialog open={!!editingBenefitCat} onOpenChange={(open) => !open && setEditingBenefitCat(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base font-semibold">Edit Benefit Category</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 py-2 text-xs">
            <div className="space-y-1">
              <label className="field-label">Category Name *</label>
              <Input
                value={editBCatName}
                onChange={(e) => setEditBCatName(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Identifier Slug</label>
              <Input
                value={editBCatSlug}
                onChange={(e) => setEditBCatSlug(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
            <div className="space-y-1">
              <label className="field-label">Description</label>
              <Input
                value={editBCatDesc}
                onChange={(e) => setEditBCatDesc(e.target.value)}
                className="h-8 text-xs"
              />
            </div>
          </div>
          <DialogFooter>
            <Button size="xs" variant="outline" onClick={() => setEditingBenefitCat(null)}>
              Cancel
            </Button>
            <Button
              size="xs"
              variant="accent"
              disabled={isUpdatingBenefitCat}
              onClick={handleSaveEditBenefitCat}
              className="gap-1"
            >
              {isUpdatingBenefitCat ? <Loader2 className="size-3 animate-spin" /> : null}
              <span>Save Changes</span>
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

export function SettingsPage() {
  return (
    <Suspense fallback={<div className="page p-8 text-xs text-muted-foreground">Loading settings...</div>}>
      <SettingsContent />
    </Suspense>
  );
}
