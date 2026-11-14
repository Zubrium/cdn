import { memo, useCallback, useEffect, useMemo, useRef, useState, type MouseEvent as ReactMouseEvent } from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  ReactFlowProvider,
  applyEdgeChanges,
  applyNodeChanges,
  useReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
  type NodeProps,
  type NodeTypes,
} from "@xyflow/react";
import { Plus, Table2 } from "lucide-react";
import "@xyflow/react/dist/style.css";
import type { DataModel, DataModelEntity, DataModelField } from "@/types";
import { DATA_MODEL_ADD_ENTITY_CONTROL_ID, positionFor } from "./model-operations";

type EntityNodeData = {
  entity: DataModelEntity;
};

type EntityFlowNode = Node<EntityNodeData, "entity">;

function fieldTypeLabel(field: DataModelField): string {
  const labels: Record<DataModelField["type"], string> = {
    text: "text",
    longText: "long text",
    integer: "integer",
    number: "number",
    boolean: "boolean",
    date: "date",
    dateTime: "datetime",
    url: "url",
    email: "email",
    enum: "enum",
    media: "file",
  };
  return labels[field.type];
}

const DataModelTableNode = memo(function DataModelTableNode({ data, selected }: NodeProps<EntityFlowNode>) {
  const { entity } = data;
  const visibleFields = entity.fields.slice(0, 6);
  return <article className={`w-60 overflow-hidden rounded-md border bg-background shadow-sm transition-[border-color,box-shadow] ${selected ? "border-primary shadow-md" : "border-border"}`}>
    <Handle type="target" position={Position.Left} className="!size-2.5 !border-2 !border-background !bg-muted-foreground" />
    <div className="border-b border-border bg-muted/45 px-3 py-2.5">
      <p className="truncate text-sm font-semibold">{entity.name || "Без названия"}</p>
      <p className="mt-0.5 text-[11px] text-muted-foreground">{entity.cardinality === "singleton" ? "Одна запись" : "Много записей"} · {entity.fields.length} пол.</p>
    </div>
    <div className="divide-y divide-border/70">
      {visibleFields.map((field) => <div key={field.id} className="flex items-center gap-2 px-3 py-1.5 text-xs">
        <span className={`min-w-0 flex-1 truncate ${field.unique ? "font-medium" : ""}`}>{field.name || "Без названия"}</span>
        <span className="shrink-0 text-muted-foreground">{fieldTypeLabel(field)}</span>
        {field.unique && <span className="text-[10px] text-warning">unique</span>}
      </div>)}
      {entity.fields.length === 0 && <p className="px-3 py-3 text-xs text-muted-foreground">Нет полей</p>}
      {entity.fields.length > visibleFields.length && <p className="px-3 py-1.5 text-[11px] text-muted-foreground">Ещё {entity.fields.length - visibleFields.length}</p>}
    </div>
    <Handle type="source" position={Position.Right} className="!size-2.5 !border-2 !border-background !bg-foreground" />
  </article>;
});

const nodeTypes: NodeTypes = {
  entity: DataModelTableNode,
};

function DataModelFlow({
  model,
  selectedEntityId,
  selectedRelationId,
  onSelectEntity,
  onSelectRelation,
  onMoveEntity,
  onCreateEntity,
  onCreateRelation,
  onOpenRelationDialog,
}: {
  model: DataModel;
  selectedEntityId?: string;
  selectedRelationId?: string;
  onSelectEntity: (entityId: string) => void;
  onSelectRelation: (relationId: string) => void;
  onMoveEntity: (entityId: string, x: number, y: number) => void;
  onCreateEntity: (x: number, y: number) => void;
  onCreateRelation: (sourceEntityId: string, targetEntityId: string) => void;
  onOpenRelationDialog: () => void;
}) {
  const [colorMode, setColorMode] = useState<"light" | "dark">(() =>
    typeof document !== "undefined" && document.documentElement.classList.contains("dark") ? "dark" : "light",
  );
  const { screenToFlowPosition } = useReactFlow();
  const initialNodes = useMemo<EntityFlowNode[]>(() => model.entities.map((entity, index) => ({
    id: entity.id,
    type: "entity",
    position: positionFor(model, entity, index),
    data: { entity },
    selected: entity.id === selectedEntityId,
  })), [model, selectedEntityId]);
  const initialEdges = useMemo<Edge[]>(() => (model.relations ?? []).map((relation) => ({
    id: relation.id,
    source: relation.fromEntityId,
    target: relation.toEntityId,
    type: "smoothstep",
    markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
    label: relation.name,
    selected: relation.id === selectedRelationId,
    style: { strokeWidth: relation.id === selectedRelationId ? 2.5 : 1.6 },
    labelStyle: { fontSize: 11 },
    labelBgPadding: [5, 3],
  })), [model.relations, selectedRelationId]);
  const [nodes, setNodes] = useState<EntityFlowNode[]>(initialNodes);
  const [edges, setEdges] = useState<Edge[]>(initialEdges);
  const [contextMenu, setContextMenu] = useState<{ clientX: number; clientY: number; flowX: number; flowY: number } | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => setNodes(initialNodes), [initialNodes]);
  useEffect(() => setEdges(initialEdges), [initialEdges]);
  useEffect(() => {
    if (typeof MutationObserver === "undefined") return;
    const root = document.documentElement;
    const sync = () => setColorMode(root.classList.contains("dark") ? "dark" : "light");
    sync();
    const observer = new MutationObserver(sync);
    observer.observe(root, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!contextMenu) return;
    const close = (event: Event) => {
      if (event.type === "keydown" && (event as KeyboardEvent).key !== "Escape") return;
      if (event.type === "pointerdown" && event.target instanceof Element && event.target.closest("[data-model-context-menu]")) return;
      setContextMenu(null);
    };
    const frame = requestAnimationFrame(() => menuRef.current?.querySelector<HTMLButtonElement>("button")?.focus());
    document.addEventListener("pointerdown", close);
    document.addEventListener("keydown", close);
    return () => {
      cancelAnimationFrame(frame);
      document.removeEventListener("pointerdown", close);
      document.removeEventListener("keydown", close);
    };
  }, [contextMenu]);

  const onNodesChange = useCallback((changes: NodeChange<EntityFlowNode>[]) => setNodes((current) => applyNodeChanges(changes, current)), []);
  const onEdgesChange = useCallback((changes: EdgeChange[]) => setEdges((current) => applyEdgeChanges(changes, current)), []);
  const onConnect = useCallback((connection: Connection) => {
    if (connection.source && connection.target && connection.source !== connection.target) onCreateRelation(connection.source, connection.target);
  }, [onCreateRelation]);
  const paneContextMenu = (event: MouseEvent | ReactMouseEvent) => {
    event.preventDefault();
    const point = screenToFlowPosition({ x: event.clientX, y: event.clientY });
    setContextMenu({ clientX: event.clientX, clientY: event.clientY, flowX: point.x, flowY: point.y });
  };

  return <section className="relative h-full min-h-0 w-full" aria-label="ER-канвас модели данных">
    <ReactFlow<EntityFlowNode, Edge>
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      colorMode={colorMode}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onConnect={onConnect}
      onNodeClick={(_, node) => { setContextMenu(null); onSelectEntity(node.id); }}
      onEdgeClick={(_, edge) => { setContextMenu(null); onSelectRelation(edge.id); }}
      onNodeDragStop={(_, node) => onMoveEntity(node.id, Math.round(node.position.x), Math.round(node.position.y))}
      onPaneClick={() => setContextMenu(null)}
      onPaneContextMenu={paneContextMenu}
      fitView
      fitViewOptions={{ padding: 0.25, maxZoom: 1.1 }}
      minZoom={0.2}
      maxZoom={1.6}
      defaultEdgeOptions={{ type: "smoothstep" }}
      proOptions={{ hideAttribution: true }}
      selectionOnDrag={false}
      panOnDrag={[0, 1, 2]}
      zoomOnScroll={false}
      className="bg-transparent"
    >
      <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
      <Controls position="bottom-left" showInteractive={false} />
    </ReactFlow>

    <div className="pointer-events-none absolute left-4 top-4 z-20 flex gap-1">
      <button id={DATA_MODEL_ADD_ENTITY_CONTROL_ID} type="button" className="pointer-events-auto inline-flex min-h-11 items-center gap-2 rounded-md border border-border bg-background/95 px-3 text-sm font-medium shadow-sm hover:bg-muted" onClick={() => onCreateEntity(80, 80)}><Plus className="size-4" />Таблица</button>
      <button type="button" className="pointer-events-auto inline-flex min-h-11 items-center gap-2 rounded-md border border-border bg-background/95 px-3 text-sm shadow-sm hover:bg-muted" disabled={model.entities.length < 2} onClick={onOpenRelationDialog}><Table2 className="size-4" />Связь</button>
    </div>
    <div className="pointer-events-none absolute bottom-5 right-5 z-20 max-w-64 text-right text-xs leading-5 text-muted-foreground">
      ПКМ по пустому месту создаёт таблицу. Потяните точку справа к точке слева другой таблицы, чтобы создать связь.
    </div>

    {contextMenu && <div ref={menuRef} data-model-context-menu role="menu" aria-label="Действия на канвасе" className="fixed z-50 min-w-52 rounded-md border border-border bg-popover p-1 shadow-lg" style={{ left: contextMenu.clientX, top: contextMenu.clientY }}>
      <button type="button" role="menuitem" className="flex min-h-11 w-full items-center gap-2 rounded px-3 text-left text-sm hover:bg-accent" onClick={() => {
        onCreateEntity(Math.round(contextMenu.flowX), Math.round(contextMenu.flowY));
        setContextMenu(null);
      }}><Plus className="size-4" />Создать таблицу</button>
      <button type="button" role="menuitem" className="flex min-h-11 w-full items-center gap-2 rounded px-3 text-left text-sm hover:bg-accent disabled:opacity-50" disabled={model.entities.length < 2} onClick={() => {
        setContextMenu(null);
        onOpenRelationDialog();
      }}><Table2 className="size-4" />Создать связь</button>
    </div>}
  </section>;
}

export function DataModelGraphCanvas(props: Parameters<typeof DataModelFlow>[0]) {
  return <ReactFlowProvider><DataModelFlow {...props} /></ReactFlowProvider>;
}
