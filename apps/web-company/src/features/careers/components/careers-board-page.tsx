"use client";

import { useState, useEffect } from "react";
import Link from "next/link";
import Image from "next/image";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Card } from "@/components/ui/card";
import {
  Search,
  MapPin,
  Building2,
  ArrowRight,
  Loader2,
  RotateCcw,
  Briefcase,
  Layers,
  X,
} from "lucide-react";
import {
  getPublishedJobs,
  getPublicOrganization,
  getPublicJobFilters,
} from "@/features/careers/server/public-queries";

export function CareersBoardPage() {
  const [search, setSearch] = useState("");
  const [selectedDept, setSelectedDept] = useState("All");
  const [selectedLocation, setSelectedLocation] = useState("All");
  const [selectedWorkMode, setSelectedWorkMode] = useState("All");
  const [selectedEmploymentType, setSelectedEmploymentType] = useState("All");
  const [selectedExpLevel, setSelectedExpLevel] = useState("All");

  const [jobs, setJobs] = useState<any[]>([]);
  const [org, setOrg] = useState<any>(null);
  const [departmentsList, setDepartmentsList] = useState<any[]>([]);
  const [locationsList, setLocationsList] = useState<any[]>([]);
  const [workModesList, setWorkModesList] = useState<any[]>([]);
  const [employmentTypesList, setEmploymentTypesList] = useState<any[]>([]);
  const [experienceLevelsList, setExperienceLevelsList] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    async function load() {
      try {
        const [jList, orgData, filters] = await Promise.all([
          getPublishedJobs(),
          getPublicOrganization(),
          getPublicJobFilters(),
        ]);
        setJobs(jList);
        setOrg(orgData);
        setDepartmentsList(filters.departments || []);
        setLocationsList(filters.locations || []);
        setWorkModesList(filters.workModes || []);
        setEmploymentTypesList(filters.employmentTypes || []);
        setExperienceLevelsList(filters.experienceLevels || []);
      } catch (err) {
        console.error(err);
      } finally {
        setLoading(false);
      }
    }
    load();
  }, []);

  // Compute dynamic options combined with master data
  const deptOptions = Array.from(
    new Set([
      ...departmentsList.map((d) => d.name),
      ...jobs.map((j) => j.departmentName).filter(Boolean),
    ]),
  );

  const locationOptions = Array.from(
    new Set([
      ...locationsList.map((l) => l.name),
      ...jobs.map((j) => j.locationName || j.locationText).filter(Boolean),
    ]),
  );

  const workModeOptions = workModesList.length > 0
    ? workModesList
    : [
        { slug: "in_office", name: "In Office" },
        { slug: "hybrid", name: "Hybrid" },
        { slug: "remote", name: "Remote" },
        { slug: "flexible", name: "Flexible" },
      ];

  const employmentTypeOptions = employmentTypesList.length > 0
    ? employmentTypesList
    : [
        { slug: "full_time", name: "Full Time" },
        { slug: "part_time", name: "Part Time" },
        { slug: "contract", name: "Contract" },
        { slug: "internship", name: "Internship" },
        { slug: "temporary", name: "Temporary" },
      ];

  const expLevelOptions = experienceLevelsList.length > 0
    ? experienceLevelsList
    : [
        { slug: "entry", name: "Junior / Entry" },
        { slug: "mid", name: "Mid-Level" },
        { slug: "senior", name: "Senior" },
        { slug: "lead", name: "Lead / Principal" },
        { slug: "director", name: "Director" },
        { slug: "executive", name: "Executive" },
      ];

  const hasActiveFilters =
    Boolean(search.trim()) ||
    selectedDept !== "All" ||
    selectedLocation !== "All" ||
    selectedWorkMode !== "All" ||
    selectedEmploymentType !== "All" ||
    selectedExpLevel !== "All";

  const resetFilters = () => {
    setSearch("");
    setSelectedDept("All");
    setSelectedLocation("All");
    setSelectedWorkMode("All");
    setSelectedEmploymentType("All");
    setSelectedExpLevel("All");
  };

  const filtered = jobs.filter((j) => {
    const q = search.toLowerCase().trim();
    const matchSearch =
      !q ||
      j.title.toLowerCase().includes(q) ||
      (j.departmentName && j.departmentName.toLowerCase().includes(q)) ||
      (j.locationName && j.locationName.toLowerCase().includes(q)) ||
      (j.locationText && j.locationText.toLowerCase().includes(q)) ||
      (Array.isArray(j.skills) && j.skills.some((s: string) => s.toLowerCase().includes(q)));

    const matchDept = selectedDept === "All" || j.departmentName === selectedDept;
    const matchLoc =
      selectedLocation === "All" ||
      j.locationName === selectedLocation ||
      j.locationText === selectedLocation;
    const matchWorkMode = selectedWorkMode === "All" || j.workMode === selectedWorkMode;
    const matchEmpType =
      selectedEmploymentType === "All" || j.employmentType === selectedEmploymentType;
    const matchExp = selectedExpLevel === "All" || j.experienceLevel === selectedExpLevel;

    return matchSearch && matchDept && matchLoc && matchWorkMode && matchEmpType && matchExp;
  });

  return (
    <div className="min-h-screen bg-background text-foreground flex flex-col">
      {/* Public Header */}
      <header className="border-b border-border bg-card/80 backdrop-blur sticky top-0 z-30">
        <div className="max-w-6xl mx-auto px-4 sm:px-6 h-14 flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <Image
              src="/logo.png"
              alt="My Organisation Logo"
              width={32}
              height={32}
              className="size-8 rounded-xs object-contain"
            />
            <div>
              <span className="font-semibold text-sm text-foreground block leading-tight">
                {org?.name || "My Organisation"}
              </span>
              <span className="text-[10px] text-muted-foreground uppercase tracking-widest">
                Careers &amp; Opportunities
              </span>
            </div>
          </div>
        </div>
      </header>

      {/* Hero Section */}
      <section className="bg-bark text-parchment py-12 px-4 sm:px-6 border-b border-bark-muted">
        <div className="max-w-4xl mx-auto text-center space-y-3">
          <Badge
            variant="outline"
            className="border-copper text-copper bg-copper/10 px-2.5 py-0.5 text-xs"
          >
            We Are Hiring
          </Badge>
          <h1 className="text-2xl sm:text-4xl font-semibold tracking-tight text-parchment">
            Build High-Impact Systems at {org?.name || "My Organisation"}
          </h1>
          <p className="text-xs sm:text-sm text-parchment/80 max-w-2xl mx-auto leading-relaxed">
            Join a forward-thinking global team building scalable enterprise infrastructure, delightful user interfaces, and next-generation applications.
          </p>
        </div>
      </section>

      {/* Job Search & Filter Options Bar */}
      <div className="max-w-6xl mx-auto px-4 sm:px-6 py-8 flex-1 w-full space-y-6">
        <div className="p-3.5 sm:p-4 rounded-xs border border-border bg-card/60 space-y-3">
          <div className="flex flex-col lg:flex-row items-stretch lg:items-center gap-2.5">
            {/* Search Input */}
            <div className="relative flex-1 min-w-[220px]">
              <Search className="absolute left-3 top-2.5 size-4 text-muted-foreground" />
              <Input
                placeholder="Search jobs..."
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                className="pl-9 h-9 text-xs bg-card"
              />
              {search && (
                <button
                  type="button"
                  onClick={() => setSearch("")}
                  className="absolute right-2.5 top-2.5 text-muted-foreground hover:text-foreground text-xs cursor-pointer"
                >
                  <X className="size-3.5" />
                </button>
              )}
            </div>

            {/* Filter Fields right to searchbar */}
            <div className="flex flex-wrap items-center gap-2">
              {/* Department Filter */}
              <select
                value={selectedDept}
                onChange={(e) => setSelectedDept(e.target.value)}
                className="h-9 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper cursor-pointer min-w-[130px]"
              >
                <option value="All">All Departments</option>
                {deptOptions.map((dept) => (
                  <option key={dept} value={dept}>
                    {dept}
                  </option>
                ))}
              </select>

              {/* Location Filter */}
              <select
                value={selectedLocation}
                onChange={(e) => setSelectedLocation(e.target.value)}
                className="h-9 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper cursor-pointer min-w-[125px]"
              >
                <option value="All">All Locations</option>
                {locationOptions.map((loc) => (
                  <option key={loc} value={loc}>
                    {loc}
                  </option>
                ))}
              </select>

              {/* Work Mode Filter */}
              <select
                value={selectedWorkMode}
                onChange={(e) => setSelectedWorkMode(e.target.value)}
                className="h-9 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper cursor-pointer min-w-[125px]"
              >
                <option value="All">All Work Modes</option>
                {workModeOptions.map((wm) => (
                  <option key={wm.slug || wm.id} value={wm.slug || wm.name.toLowerCase().replace(/ /g, "_")}>
                    {wm.name}
                  </option>
                ))}
              </select>

              {/* Employment Type Filter */}
              <select
                value={selectedEmploymentType}
                onChange={(e) => setSelectedEmploymentType(e.target.value)}
                className="h-9 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper cursor-pointer min-w-[125px]"
              >
                <option value="All">All Job Types</option>
                {employmentTypeOptions.map((et) => (
                  <option key={et.slug || et.id} value={et.slug || et.name.toLowerCase().replace(/ /g, "_")}>
                    {et.name}
                  </option>
                ))}
              </select>

              {/* Experience Level Filter */}
              <select
                value={selectedExpLevel}
                onChange={(e) => setSelectedExpLevel(e.target.value)}
                className="h-9 px-2.5 text-xs rounded-xs border border-border bg-card text-foreground focus:outline-none focus:ring-1 focus:ring-copper cursor-pointer min-w-[125px]"
              >
                <option value="All">All Experience</option>
                {expLevelOptions.map((exp) => (
                  <option key={exp.slug || exp.id} value={exp.slug || exp.name.toLowerCase().replace(/ /g, "_")}>
                    {exp.name}
                  </option>
                ))}
              </select>

              {/* Reset Filters Button */}
              {hasActiveFilters && (
                <Button
                  type="button"
                  variant="ghost"
                  size="xs"
                  onClick={resetFilters}
                  className="h-9 text-xs text-muted-foreground hover:text-foreground gap-1 px-2.5 cursor-pointer"
                  title="Reset all search & filter options"
                >
                  <RotateCcw className="size-3 text-copper" />
                  <span>Reset</span>
                </Button>
              )}
            </div>
          </div>

          {/* Active Filter Chips & Summary */}
          <div className="flex items-center justify-between text-xs text-muted-foreground pt-1 flex-wrap gap-2 border-t border-border/50">
            <div className="flex items-center gap-1.5 flex-wrap">
              <span>
                Showing <strong className="text-foreground">{filtered.length}</strong> of {jobs.length} open roles
              </span>
              {selectedDept !== "All" && (
                <Badge variant="outline" className="text-[10px] bg-card text-copper border-copper/30 gap-1 pr-1">
                  <span>{selectedDept}</span>
                  <button type="button" onClick={() => setSelectedDept("All")} className="hover:text-foreground cursor-pointer">×</button>
                </Badge>
              )}
              {selectedLocation !== "All" && (
                <Badge variant="outline" className="text-[10px] bg-card text-copper border-copper/30 gap-1 pr-1">
                  <span>{selectedLocation}</span>
                  <button type="button" onClick={() => setSelectedLocation("All")} className="hover:text-foreground cursor-pointer">×</button>
                </Badge>
              )}
              {selectedWorkMode !== "All" && (
                <Badge variant="outline" className="text-[10px] bg-card text-copper border-copper/30 gap-1 pr-1">
                  <span className="capitalize">{selectedWorkMode.replace(/_/g, " ")}</span>
                  <button type="button" onClick={() => setSelectedWorkMode("All")} className="hover:text-foreground cursor-pointer">×</button>
                </Badge>
              )}
              {selectedEmploymentType !== "All" && (
                <Badge variant="outline" className="text-[10px] bg-card text-copper border-copper/30 gap-1 pr-1">
                  <span className="capitalize">{selectedEmploymentType.replace(/_/g, " ")}</span>
                  <button type="button" onClick={() => setSelectedEmploymentType("All")} className="hover:text-foreground cursor-pointer">×</button>
                </Badge>
              )}
              {selectedExpLevel !== "All" && (
                <Badge variant="outline" className="text-[10px] bg-card text-copper border-copper/30 gap-1 pr-1">
                  <span className="capitalize">{selectedExpLevel.replace(/_/g, " ")} Level</span>
                  <button type="button" onClick={() => setSelectedExpLevel("All")} className="hover:text-foreground cursor-pointer">×</button>
                </Badge>
              )}
            </div>
          </div>
        </div>

        {/* Job Requisitions Grid */}
        {loading ? (
          <div className="p-16 text-center text-xs text-muted-foreground flex items-center justify-center gap-2">
            <Loader2 className="size-4 animate-spin text-copper" />
            <span>Loading career openings...</span>
          </div>
        ) : filtered.length === 0 ? (
          <div className="p-12 text-center text-xs text-muted-foreground border border-dashed border-border rounded-xs space-y-2">
            <p className="font-medium text-foreground">No open positions match your selected filter criteria.</p>
            {hasActiveFilters && (
              <Button type="button" size="xs" variant="outline" onClick={resetFilters} className="text-xs">
                Clear Filters
              </Button>
            )}
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {filtered.map((job) => (
              <Card
                key={job.id}
                className="shadow-none border border-border hover:border-copper transition-all p-4 flex flex-col justify-between group"
              >
                <div className="space-y-2.5">
                  <div className="flex items-start justify-between gap-2">
                    <div>
                      <Link href={`/careers/apply/${job.id}`}>
                        <h3 className="font-semibold text-sm text-foreground group-hover:text-copper transition-colors">
                          {job.title}
                        </h3>
                      </Link>
                      <div className="flex items-center gap-2 text-[11px] text-muted-foreground mt-0.5">
                        <span className="flex items-center gap-1">
                          <Building2 className="size-3 text-copper" />
                          <span>{job.departmentName || "Engineering"}</span>
                        </span>
                        <span>•</span>
                        <span className="flex items-center gap-1">
                          <MapPin className="size-3 text-copper" />
                          <span>{job.locationName || job.locationText}</span>
                        </span>
                      </div>
                    </div>

                    <div className="flex items-center gap-1">
                      <Badge variant="outline" className="capitalize text-[10px] bg-card border-copper/30 text-copper">
                        {job.workMode?.replace(/_/g, " ")}
                      </Badge>
                      {job.employmentType && (
                        <Badge variant="outline" className="capitalize text-[10px] bg-card border-border text-muted-foreground">
                          {job.employmentType?.replace(/_/g, " ")}
                        </Badge>
                      )}
                    </div>
                  </div>

                  <p className="text-xs text-muted-foreground line-clamp-2 leading-relaxed">
                    {job.summary
                      ? job.summary.replace(/<[^>]*>/g, " ").replace(/&nbsp;/g, " ").trim()
                      : "Join our fast growing team to solve interesting challenges at scale."}
                  </p>

                  {job.skills && Array.isArray(job.skills) && job.skills.length > 0 && (
                    <div className="flex flex-wrap gap-1 pt-1">
                      {job.skills.slice(0, 4).map((s: string) => (
                        <Badge key={s} variant="outline" className="text-[10px] px-1.5 py-0 border-border bg-muted/30 text-muted-foreground">
                          {s}
                        </Badge>
                      ))}
                      {job.skills.length > 4 && (
                        <span className="text-[10px] text-muted-foreground self-center">
                          +{job.skills.length - 4} more
                        </span>
                      )}
                    </div>
                  )}
                </div>

                <div className="pt-4 mt-3 border-t border-border flex items-center justify-between">
                  <div>
                    {job.salaryMin ? (
                      <span className="text-xs font-bold text-copper">
                        {job.currency || "$"} {(job.salaryMin || 0).toLocaleString()} – {(job.salaryMax || 0).toLocaleString()}
                        <span className="text-[10px] text-muted-foreground font-normal ml-1">
                          / {job.payFrequency === "hourly" ? "hr" : "yr"}
                        </span>
                      </span>
                    ) : (
                      <span className="text-xs text-muted-foreground">Competitive Compensation</span>
                    )}
                  </div>

                  <Link href={`/careers/apply/${job.id}`}>
                    <Button size="xs" variant="accent" className="gap-1 text-xs">
                      <span>Apply Now</span>
                      <ArrowRight className="size-3" />
                    </Button>
                  </Link>
                </div>
              </Card>
            ))}
          </div>
        )}
      </div>

      {/* Footer */}
      <footer className="border-t border-border bg-card py-6 text-center text-xs text-muted-foreground">
        © {new Date().getFullYear()} {org?.name || "My Organisation"}. Powered by Karmarat ATS &amp; HRM Platform.
      </footer>
    </div>
  );
}
