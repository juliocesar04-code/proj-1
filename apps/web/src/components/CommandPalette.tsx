import {
  Activity,
  FileText,
  GitCompareArrows,
  Keyboard,
  RefreshCw,
  Search,
  ServerCrash,
  ShieldCheck,
  X,
} from "lucide-react";
import {
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { ScenarioId } from "../api.js";
import { useI18n } from "../i18n.js";

interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRefresh: () => void;
  onScenario: (scenario: ScenarioId) => void;
  onReport: () => void;
  onCompare: () => void;
  canCompare: boolean;
}

interface Command {
  id: string;
  label: string;
  hint: string;
  icon: React.ReactNode;
  run: () => void;
  disabled?: boolean;
}

export function CommandPalette({
  open,
  onOpenChange,
  onRefresh,
  onScenario,
  onReport,
  onCompare,
  canCompare,
}: CommandPaletteProps) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const commands = useMemo<Command[]>(
    () => [
      {
        id: "refresh",
        label: t("command.refresh"),
        hint: "R",
        icon: <RefreshCw size={16} />,
        run: onRefresh,
      },
      {
        id: "report",
        label: t("command.report"),
        hint: "I",
        icon: <FileText size={16} />,
        run: onReport,
      },
      {
        id: "compare",
        label: t("command.compare"),
        hint: "C",
        icon: <GitCompareArrows size={16} />,
        run: onCompare,
        disabled: !canCompare,
      },
      {
        id: "stable",
        label: t("scenario.stable.label"),
        hint: t("command.scenario"),
        icon: <ShieldCheck size={16} />,
        run: () => onScenario("stable"),
      },
      {
        id: "payment",
        label: t("scenario.payment.label"),
        hint: t("command.scenario"),
        icon: <ServerCrash size={16} />,
        run: () => onScenario("payment-timeout"),
      },
      {
        id: "database",
        label: t("scenario.database.label"),
        hint: t("command.scenario"),
        icon: <Activity size={16} />,
        run: () => onScenario("database-lock"),
      },
      {
        id: "cache",
        label: t("scenario.cache.label"),
        hint: t("command.scenario"),
        icon: <Activity size={16} />,
        run: () => onScenario("cache-degradation"),
      },
    ],
    [
      canCompare,
      onCompare,
      onRefresh,
      onReport,
      onScenario,
      t,
    ],
  );

  const filtered = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return commands;

    return commands.filter((command) =>
      command.label.toLowerCase().includes(normalized),
    );
  }, [commands, query]);

  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if (
        (event.metaKey || event.ctrlKey) &&
        event.key.toLowerCase() === "k"
      ) {
        event.preventDefault();
        onOpenChange(!open);
      }

      if (event.key === "Escape" && open) {
        onOpenChange(false);
      }
    };

    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, [onOpenChange, open]);

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setActiveIndex(0);
    window.setTimeout(() => inputRef.current?.focus(), 0);
  }, [open]);

  useEffect(() => {
    if (!open) return undefined;

    const listener = (event: KeyboardEvent) => {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setActiveIndex((current) =>
          Math.min(filtered.length - 1, current + 1),
        );
      } else if (event.key === "ArrowUp") {
        event.preventDefault();
        setActiveIndex((current) => Math.max(0, current - 1));
      } else if (event.key === "Enter") {
        const command = filtered[activeIndex];
        if (command && !command.disabled) {
          event.preventDefault();
          command.run();
          onOpenChange(false);
        }
      }
    };

    window.addEventListener("keydown", listener);
    return () => window.removeEventListener("keydown", listener);
  }, [activeIndex, filtered, onOpenChange, open]);

  if (!open) return null;

  return (
    <div
      className="command-overlay"
      role="presentation"
      onMouseDown={() => onOpenChange(false)}
    >
      <section
        className="command-palette"
        role="dialog"
        aria-modal="true"
        aria-label={t("command.title")}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <div className="command-palette__search">
          <Search size={17} />
          <input
            ref={inputRef}
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setActiveIndex(0);
            }}
            placeholder={t("command.placeholder")}
          />
          <button
            type="button"
            className="command-palette__close"
            onClick={() => onOpenChange(false)}
          >
            <X size={15} />
          </button>
        </div>

        <div className="command-palette__list">
          {filtered.map((command, index) => (
            <button
              key={command.id}
              type="button"
              disabled={command.disabled}
              className={[
                "command-item",
                index === activeIndex
                  ? "command-item--active"
                  : "",
              ]
                .filter(Boolean)
                .join(" ")}
              onMouseEnter={() => setActiveIndex(index)}
              onClick={() => {
                command.run();
                onOpenChange(false);
              }}
            >
              <span className="command-item__icon">
                {command.icon}
              </span>
              <span>{command.label}</span>
              <kbd>{command.hint}</kbd>
            </button>
          ))}

          {filtered.length === 0 ? (
            <div className="command-palette__empty">
              {t("command.noResults")}
            </div>
          ) : null}
        </div>

        <footer className="command-palette__footer">
          <span>
            <Keyboard size={13} />
            ↑↓ {t("command.navigate")}
          </span>
          <span>↵ {t("command.run")}</span>
          <span>esc {t("command.close")}</span>
        </footer>
      </section>
    </div>
  );
}
