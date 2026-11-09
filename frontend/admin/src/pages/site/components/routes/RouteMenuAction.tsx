import type { ButtonHTMLAttributes } from "react";

export function RouteMenuAction({ className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement>) {
  return <button role="menuitem" type="button" className={`flex min-h-11 w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm outline-none hover:bg-accent focus:bg-accent disabled:pointer-events-none disabled:opacity-40 ${className}`} {...props} />;
}
