type TokensSummaryProps = {
  total: number;
  active: number;
  revoked: number;
};

export function TokensSummary({ total, active, revoked }: TokensSummaryProps) {
  const items = [
    { label: "Всего", value: total },
    { label: "Активных", value: active },
    { label: "Отозвано", value: revoked },
  ];

  return (
    <section aria-label="Сводка токенов" className="grid grid-cols-3 gap-2">
      {items.map((item) => (
        <div key={item.label} className="rounded-lg border border-border bg-card p-3">
          <p className="text-2xs text-muted-foreground">{item.label}</p>
          <p className="mt-0.5 text-lg font-semibold">{item.value}</p>
        </div>
      ))}
    </section>
  );
}
