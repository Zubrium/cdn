import { access, realpath, stat } from "node:fs/promises";
import path from "node:path";

function isWithin(root, candidate) {
  return candidate === root || candidate.startsWith(root + path.sep);
}

export async function resolveAllowedRealPath(candidate, allowedRoots) {
  if (!path.isAbsolute(candidate)) throw new Error("filesystem path must be absolute");
  const [realCandidate, ...realRoots] = await Promise.all([
    realpath(candidate),
    ...allowedRoots.map((root) => realpath(root)),
  ]);
  if (!realRoots.some((root) => isWithin(root, realCandidate))) {
    throw new Error("filesystem path resolves outside the allowed roots");
  }
  const info = await stat(realCandidate);
  if (!info.isFile()) throw new Error("filesystem path must reference a file");
  return realCandidate;
}

export async function resolveFeatureFile(directory, relativePath, label) {
  if (
    typeof relativePath !== "string" ||
    !relativePath.trim() ||
    relativePath.includes("\\") ||
    path.isAbsolute(relativePath) ||
    path.win32.isAbsolute(relativePath)
  ) {
    throw new Error(label + " must be a relative file path");
  }
  const resolved = path.resolve(directory, relativePath);
  if (!isWithin(path.resolve(directory), resolved)) {
    throw new Error(label + " must stay inside the feature directory");
  }
  await access(resolved);
  const [realDirectory, realFile, fileInfo] = await Promise.all([
    realpath(directory),
    realpath(resolved),
    stat(resolved),
  ]);
  if (!isWithin(realDirectory, realFile)) {
    throw new Error(label + " must not resolve through a symlink outside the feature directory");
  }
  if (!fileInfo.isFile()) throw new Error(label + " must reference a file");
  return resolved;
}
