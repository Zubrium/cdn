import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme, type ThemeMode } from "@/app/theme";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

const modes: ThemeMode[] = ["system", "light", "dark"];
const modeLabels: Record<ThemeMode, string> = {
  system: "Системная",
  light: "Светлая",
  dark: "Тёмная",
};

export function ThemeToggle({
  collapsed = false,
  className,
}: {
  collapsed?: boolean;
  className?: string;
}) {
  const { mode, resolved, setMode } = useTheme();
  const Icon = mode === "system" ? Monitor : mode === "light" ? Sun : Moon;
  const currentMode = mode === "system"
    ? `Системная · сейчас ${resolved === "dark" ? "тёмная" : "светлая"}`
    : modeLabels[mode];
  const nextMode = modes[(modes.indexOf(mode) + 1) % modes.length];

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={`Переключить тему. Сейчас: ${currentMode}`}
          onClick={() => setMode(nextMode)}
          className={cn("shrink-0 rounded-xl text-muted-foreground hover:text-foreground", className)}
        >
          <Icon className="size-4" aria-hidden="true" />
        </Button>
      </TooltipTrigger>
      <TooltipContent side={collapsed ? "right" : "top"}>
        Тема: {currentMode}. Нажмите, чтобы переключить.
      </TooltipContent>
    </Tooltip>
  );
}
