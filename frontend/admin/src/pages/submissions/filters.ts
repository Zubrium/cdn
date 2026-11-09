export const DEFAULT_SUBMISSION_SORT = "desc" as const;

export function submissionListHref(search: string): string {
  return `/submissions${search}`;
}

export function submissionDetailHref(id: string, search: string): string {
  return `/submissions/${encodeURIComponent(id)}${search}`;
}

export type SubmissionFilterState = {
  form: string;
  branchId: string;
  status: string;
  field: string;
  from: string;
  to: string;
  sort: string;
  archived: boolean;
  q: string;
};

export function hasActiveSubmissionFilters(filters: SubmissionFilterState): boolean {
  return Boolean(filters.form || filters.q || filters.field || filters.from || filters.to || filters.archived)
    || filters.branchId !== "all"
    || filters.status !== "all"
    || filters.sort !== DEFAULT_SUBMISSION_SORT;
}

export const RESET_SUBMISSION_FILTERS: Record<string, string | null> = {
  form: null,
  q: null,
  branchId: null,
  environment: null,
  status: null,
  field: null,
  from: null,
  to: null,
  archived: null,
  sort: null,
};
