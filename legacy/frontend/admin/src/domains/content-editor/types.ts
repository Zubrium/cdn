import type { JsonObject } from "@/types";

export type BlockType =
  | "paragraph"
  | "heading"
  | "bulletedList"
  | "numberedList"
  | "quote"
  | "code"
  | "divider"
  | "image"
  | "gallery"
  | "video";

export interface ParagraphBlock {
  id: string;
  type: "paragraph";
  data: { text: string };
}

export interface HeadingBlock {
  id: string;
  type: "heading";
  data: { text: string; level: 1 | 2 | 3 | 4 };
}

export interface ListBlock {
  id: string;
  type: "bulletedList" | "numberedList";
  data: { items: string[] };
}

export interface QuoteBlock {
  id: string;
  type: "quote";
  data: { text: string; attribution?: string };
}

export interface CodeBlock {
  id: string;
  type: "code";
  data: { code: string; language?: string };
}

export interface DividerBlock {
  id: string;
  type: "divider";
  data: Record<string, never>;
}

export interface ImageBlock {
  id: string;
  type: "image";
  data: { mediaId: string; alt: string; caption?: string };
}

export interface GalleryBlock {
  id: string;
  type: "gallery";
  data: { mediaIds: string[]; caption?: string };
}

export interface VideoBlock {
  id: string;
  type: "video";
  data: { mediaId: string; caption?: string };
}

export type ContentBlock =
  | ParagraphBlock
  | HeadingBlock
  | ListBlock
  | QuoteBlock
  | CodeBlock
  | DividerBlock
  | ImageBlock
  | GalleryBlock
  | VideoBlock;

export interface DocumentState {
  metadata: JsonObject;
  blocks: ContentBlock[];
}

export type SaveState = "idle" | "saving" | "saved" | "error" | "conflict";

export interface BlockTypeOption {
  type: BlockType;
  label: string;
  description: string;
}
