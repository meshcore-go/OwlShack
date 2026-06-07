import { PageHeader } from "@/components/PageHeader";

export function StubPage({ title }: { title: string }) {
  return (
    <div className="space-y-6">
      <PageHeader eyebrow="loading…" title={title} />
      <div className="panel p-8 text-center">
        <p className="font-mono text-sm text-muted-foreground">
          UNDER CONSTRUCTION
        </p>
      </div>
    </div>
  );
}
