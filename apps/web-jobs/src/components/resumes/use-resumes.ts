"use client";

import { useCallback, useEffect, useState } from "react";
import { isProblem } from "@karmarat/ui";
import { useApi } from "@karmarat/ui/react";

import type { DataPage, Resume, ResumeUploadTicket } from "@/lib/api-types";

/** The content types the candidates service will sign an upload for. */
export const RESUME_CONTENT_TYPES: Record<string, string> = {
  pdf: "application/pdf",
  doc: "application/msword",
  docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
};

export const RESUME_ACCEPT = ".pdf,.doc,.docx";

export interface UploadResult {
  resume: Resume;
  /** The key the service signed — the truthful one, straight from the URL. */
  objectKey: string | null;
}

export function useResumes() {
  const api = useApi();
  const [resumes, setResumes] = useState<Resume[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [uploading, setUploading] = useState(false);

  const reload = useCallback(async () => {
    try {
      const page = await api.get<DataPage<Resume>>("/api/v1/me/resumes?limit=50");
      setResumes(page.data ?? []);
      setError(null);
    } catch (cause) {
      setError(cause);
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    void reload();
  }, [reload]);

  /**
   * Uploads a file without a byte of it passing through this app.
   *
   * The service signs a single-object PUT and the browser writes straight to
   * storage. Proxying the bytes would put a multi-megabyte body through a
   * Next.js route handler for no benefit, and would mean this app held a
   * storage credential it has no business holding.
   */
  const upload = useCallback(
    async (file: File): Promise<UploadResult> => {
      setUploading(true);
      try {
        const extension = file.name.split(".").pop()?.toLowerCase() ?? "";
        const contentType = RESUME_CONTENT_TYPES[extension] ?? file.type;

        const ticket = await api.post<ResumeUploadTicket>(
          "/api/v1/me/resumes/upload-url",
          { filename: file.name, contentType, sizeBytes: file.size },
        );

        const response = await fetch(ticket.uploadUrl, {
          method: ticket.method || "PUT",
          // The signature covers Content-Type, so a mismatch here is rejected
          // by the bucket rather than by us — send exactly what was signed.
          headers: { "Content-Type": ticket.headers["Content-Type"] },
          body: file,
        });

        if (!response.ok) {
          throw new Error(
            "The file could not be uploaded to storage. Please try again.",
          );
        }

        await reload();

        const { extractKeyFromUploadUrl } = await import("@/lib/resume-key");
        return {
          resume: ticket.resume,
          objectKey: extractKeyFromUploadUrl(ticket.uploadUrl),
        };
      } finally {
        setUploading(false);
      }
    },
    [api, reload],
  );

  const remove = useCallback(
    async (id: string) => {
      await api.delete(`/api/v1/me/resumes/${id}`);
      await reload();
    },
    [api, reload],
  );

  const setPrimary = useCallback(
    async (id: string) => {
      await api.post(`/api/v1/me/resumes/${id}/primary`);
      await reload();
    },
    [api, reload],
  );

  return {
    resumes,
    loading,
    uploading,
    error,
    errorMessage: isProblem(error) ? error.detail : null,
    reload,
    upload,
    remove,
    setPrimary,
  };
}
