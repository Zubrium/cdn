export function readySnapshots<T extends { state: string }>(snapshots: readonly T[]): T[] {
  return snapshots.filter((snapshot) => snapshot.state === "ready");
}
