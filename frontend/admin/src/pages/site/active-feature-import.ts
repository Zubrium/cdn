export interface ActiveFeatureImport {
  principalID: string;
  id: string;
}

export function activeFeatureImportIDForPrincipal(
  activeImport: ActiveFeatureImport | null,
  principalID: string | undefined,
): string | null {
  if (!activeImport || !principalID || activeImport.principalID !== principalID) return null;
  return activeImport.id;
}
