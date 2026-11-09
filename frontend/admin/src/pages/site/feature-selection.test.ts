import { describe, expect, it } from "vitest";
import type { SiteConfig } from "@/types";
import {
  deriveFeatureComposition,
  featureVersionChanges,
  normalizeFeatureComposition,
  removePlacement,
  selectFeature,
  selectedFeatures,
} from "./feature-selection";

const config: SiteConfig = {
  formatVersion: 3,
  features: [
    { featureKey: "legacy-unplaced", version: "9.9.9" },
    { featureKey: "catalog", version: "1.0.0" },
  ],
  pages: [
    { id: "home", name: "Home", kind: "page", placements: [
      { id: "main-data", slot: "main", build: { featureKey: "main-data", version: "1.0.0" }, dataBindings: [
        { inputKey: "item", entityId: "entity-product", fieldId: "field-title", relationIds: ["relation-category"] },
      ] },
      { id: "main-data-copy", slot: "footer", build: { featureKey: "main-data", version: "1.0.0" } },
    ] },
    { id: "product", name: "Product", kind: "detail", placements: [{
      id: "detail", slot: "main", build: { featureKey: "detail", version: "1.0.0" },
      arguments: { slug: { source: "path", pathArg: "slug" } },
      dataBindings: [{ inputKey: "product", entityId: "entity-product", fields: [{ inputPath: "/title", fieldId: "field-title" }], lookup: { argument: "slug", fieldId: "field-slug" } }],
    }] },
    { id: "catalog-page", name: "Catalog", kind: "collection-list", placements: [{
      id: "catalog", slot: "main", build: { featureKey: "catalog", version: "1.0.0" },
    }] },
  ],
  routeTree: { id: "root", type: "site", children: [
    { id: "product-route", type: "path", path: "/product/:slug", pageId: "product" },
  ] },
};

describe("placement-derived feature composition", () => {
  it("derives unique pinned builds from placements and drops unplaced legacy selections", () => {
    expect(deriveFeatureComposition(config)).toEqual([
      { featureKey: "main-data", version: "1.0.0" },
      { featureKey: "detail", version: "1.0.0" },
      { featureKey: "catalog", version: "1.0.0" },
    ]);
    expect([...selectedFeatures(config)]).toEqual([
      ["main-data", "1.0.0"],
      ["detail", "1.0.0"],
      ["catalog", "1.0.0"],
    ]);
    expect(normalizeFeatureComposition(config).features).toEqual(deriveFeatureComposition(config));
    expect(config.features).toContainEqual({ featureKey: "legacy-unplaced", version: "9.9.9" });
  });

  it("deduplicates identical placement references while preserving first-use order", () => {
    const composition = deriveFeatureComposition(config);
    expect(composition.filter((item) => item.featureKey === "main-data")).toEqual([
      { featureKey: "main-data", version: "1.0.0" },
    ]);
    expect(composition).toHaveLength(3);
  });

  it("updates every placement of a build without mutating bindings or the parent config", () => {
    const updated = selectFeature(config, "catalog", "2.0.0");
    expect(updated.features).toContainEqual({ featureKey: "catalog", version: "2.0.0" });
    expect(updated.pages[0]?.placements[0]?.dataBindings).toEqual(config.pages[0]?.placements[0]?.dataBindings);
    expect(updated.pages[2]?.placements[0]?.build.version).toBe("2.0.0");
    expect(updated.pages[1]?.placements[0]?.dataBindings).toEqual(config.pages[1]?.placements[0]?.dataBindings);
    expect(featureVersionChanges(config, updated)).toEqual([{ featureKey: "catalog", fromVersion: "1.0.0", toVersion: "2.0.0" }]);
    expect(config.pages[2]?.placements[0]?.build.version).toBe("1.0.0");
    expect(config.features).toContainEqual({ featureKey: "catalog", version: "1.0.0" });
  });

  it("removes a placement without tying shared model records to a source feature", () => {
    const result = removePlacement(config, "catalog-page", "catalog");
    expect(result.pages[2]?.placements).toHaveLength(0);
    expect(result.features).toEqual([{ featureKey: "main-data", version: "1.0.0" }, { featureKey: "detail", version: "1.0.0" }]);
    expect(config.pages[2]?.placements).toHaveLength(1);
  });

  it("allows removing one duplicate source placement while the exact build remains placed", () => {
    const withDuplicate = structuredClone(config);
    withDuplicate.pages[2]!.placements.push({ id: "catalog-secondary", slot: "footer", build: { featureKey: "catalog", version: "1.0.0" } });
    const result = removePlacement(withDuplicate, "catalog-page", "catalog");
    expect(result.pages[2]?.placements.map((placement) => placement.id)).toEqual(["catalog-secondary"]);
    expect(result.features).toContainEqual({ featureKey: "catalog", version: "1.0.0" });
    expect(withDuplicate.pages[2]?.placements).toHaveLength(2);
  });

  it("removes a placement and normalizes the child composition without legacy relation cleanup", () => {
    const result = removePlacement(config, "home", "main-data");
    expect(result.pages[0]?.placements.map((placement) => placement.id)).toEqual(["main-data-copy"]);
    expect(result.features).toEqual([
      { featureKey: "main-data", version: "1.0.0" },
      { featureKey: "detail", version: "1.0.0" },
      { featureKey: "catalog", version: "1.0.0" },
    ]);
    expect(config.pages[0]?.placements).toHaveLength(2);
    expect(config.pages[0]?.placements[0]?.dataBindings).toEqual([
      { inputKey: "item", entityId: "entity-product", fieldId: "field-title", relationIds: ["relation-category"] },
    ]);
  });
});
