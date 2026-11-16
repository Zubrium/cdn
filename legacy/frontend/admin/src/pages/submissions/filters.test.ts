import { describe, expect, it } from "vitest";
import {
  DEFAULT_SUBMISSION_SORT,
  hasActiveSubmissionFilters,
  RESET_SUBMISSION_FILTERS,
  submissionDetailHref,
  submissionListHref,
} from "./filters";

const cleanFilters = {
  form: "",
  branchId: "all",
  status: "all",
  field: "",
  from: "",
  to: "",
  sort: DEFAULT_SUBMISSION_SORT,
  archived: false,
  q: "",
};

describe("submission filters", () => {
  it("does not show reset for defaults, but does for form or sort selections", () => {
    expect(hasActiveSubmissionFilters(cleanFilters)).toBe(false);
    expect(hasActiveSubmissionFilters({ ...cleanFilters, form: "contact" })).toBe(true);
    expect(hasActiveSubmissionFilters({ ...cleanFilters, sort: "asc" })).toBe(true);
  });

  it("clears form and sort along with the rest of the filter state", () => {
    expect(RESET_SUBMISSION_FILTERS).toMatchObject({ form: null, sort: null, branchId: null, field: null });
  });

  it("preserves every active filter when opening a submission and returning", () => {
    const search = "?branchId=branch-id&status=in_progress&q=Ivan&from=2026-11-01&sort=asc";
    const detailHref = submissionDetailHref("submission/id", search);

    expect(detailHref).toBe(`/submissions/submission%2Fid${search}`);
    expect(submissionListHref(search)).toBe(`/submissions${search}`);
  });
});
