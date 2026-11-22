export const pageSize = 100;

export const schemaQueryKey = ["formsEngine", "schemas"] as const;
export const schemaDetailQueryKey = (schemaID: string) => ["formsEngine", "schema", schemaID] as const;
export const canonicalUUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
