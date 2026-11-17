// Stable test and extension surface for feature form controls. Page and feature
// screens live in PageDetailPage and FeatureEditorPage and are route-owned.
export {
  addArgumentArrayItem,
  ArgumentField,
  moveArgumentArrayItem,
  removeArgumentArrayItem,
  updateArgumentArrayItem,
} from "./components/page-editor/FeatureArgumentsEditor";
export {
  DetailSEOFieldsEditor,
  updateDetailSEOField,
} from "./components/page-editor/StructuralBindingsEditor";
export type { SchemaField } from "./components/page-editor/schema";
