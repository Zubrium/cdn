export function redirectLocation(to: string, search: string, hash: string): string {
  return `${to}${search}${hash}`;
}
