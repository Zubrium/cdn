export type RouteDialogState = {
  mode: "root" | "child" | "edit";
  parentId?: string;
  routeId?: string;
  existingPageId?: string;
  chooseExisting?: boolean;
};
