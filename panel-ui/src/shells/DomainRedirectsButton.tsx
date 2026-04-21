// Redirects modal for per-domain and per-page redirects.
// Opens a modal with two sections:
// 1. Whole-domain redirect toggle + URL + type selector
// 2. Page-level redirects list with add/delete controls (v2: drag-reorder, active toggle, wildcard)
import { useState } from "react";
import {
  SwapOutlined,
  CheckOutlined,
  DeleteOutlined,
  PlusOutlined,
  DragOutlined,
  DownOutlined,
  UpOutlined,
} from "@ant-design/icons";
import {
  Button,
  Modal,
  Switch,
  Row,
  Col,
  Input,
  Select,
  Card,
  Typography,
  notification,
} from "antd";
import { useQueryClient } from "@tanstack/react-query";
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
  useSortable,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";

import { apiClient } from "../apiClient";

export type PageRedirect = {
  source: string;
  destination: string;
  type: "301" | "302" | "307" | "308";
  active?: boolean;
  wildcard?: boolean;
};

export type DomainRedirectsTarget = {
  id: string;
  name: string;
  redirect_all_to?: string | null;
  redirect_all_type?: string | null;
  page_redirects?: PageRedirect[] | null;
};

const REDIRECT_TYPE_OPTIONS = [
  { value: "301", label: "Permanent (301) - SEO friendly" },
  { value: "302", label: "Temporary (302)" },
  { value: "307", label: "Temporary (307) - preserves method" },
  { value: "308", label: "Permanent (308) - preserves method" },
];

// Sortable card item for drag-reorder
interface SortableCardProps {
  idx: number;
  pr: PageRedirect;
  isExpanded: boolean;
  onToggleExpanded: (idx: number) => void;
  onRemove: (idx: number) => void;
  onUpdate: (idx: number, key: keyof PageRedirect, value: string | boolean) => void;
}

const SortableCard = ({
  idx,
  pr,
  isExpanded,
  onToggleExpanded,
  onRemove,
  onUpdate,
}: SortableCardProps) => {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: idx,
  });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  };

  return (
    <div ref={setNodeRef} style={style}>
      <Card
        style={{ marginBottom: 12 }}
        bodyStyle={{ padding: 12 }}
      >
        <div style={{ display: "flex", alignItems: "center", marginBottom: 12, gap: 8 }}>
          <button
            {...attributes}
            {...listeners}
            style={{
              cursor: "grab",
              background: "none",
              border: "none",
              padding: 4,
              display: "flex",
              alignItems: "center",
            }}
          >
            <DragOutlined />
          </button>

          <Switch
            checked={pr.active ?? true}
            onChange={(checked) => onUpdate(idx, "active", checked)}
            style={{ marginRight: 4 }}
          />
          <Typography.Text type="secondary">
            Active
          </Typography.Text>

          <div style={{ flex: 1 }} />

          <Typography.Text type="secondary">
            → {pr.type}
          </Typography.Text>
          <Button
            type="text"
            icon={isExpanded ? <UpOutlined /> : <DownOutlined />}
            onClick={() => onToggleExpanded(idx)}
            style={{ padding: 4 }}
          />
          <Button
            danger
            icon={<DeleteOutlined />}
            type="text"
            onClick={() => onRemove(idx)}
          />
        </div>

        {isExpanded && (
          <>
            <Row gutter={16} style={{ marginBottom: 12 }}>
              <Col span={12}>
                <div style={{ marginBottom: 8 }}>
                  <Typography.Text>
                    Source Path <Typography.Text type="danger">*</Typography.Text>
                  </Typography.Text>
                </div>
                <Input
                  placeholder="/old-page"
                  value={pr.source}
                  onChange={(e) => onUpdate(idx, "source", e.target.value)}
                />
                <Typography.Text type="secondary" style={{ display: "block", marginTop: 4 }}>
                  Path to redirect from (e.g., /old-page)
                </Typography.Text>
              </Col>
              <Col span={12}>
                <div style={{ marginBottom: 8 }}>
                  <Typography.Text>
                    Destination URL <Typography.Text type="danger">*</Typography.Text>
                  </Typography.Text>
                </div>
                <Input
                  placeholder="https://example.com/new-page"
                  value={pr.destination}
                  onChange={(e) => onUpdate(idx, "destination", e.target.value)}
                />
                <Typography.Text type="secondary" style={{ display: "block", marginTop: 4 }}>
                  Full URL to redirect to
                </Typography.Text>
              </Col>
            </Row>

            <Row gutter={16} style={{ marginBottom: 12 }}>
              <Col span={12}>
                <div style={{ marginBottom: 8 }}>
                  <Typography.Text>
                    Type <Typography.Text type="danger">*</Typography.Text>
                  </Typography.Text>
                </div>
                <Select
                  value={pr.type}
                  onChange={(value) => onUpdate(idx, "type", value)}
                  options={REDIRECT_TYPE_OPTIONS}
                  style={{ width: "100%" }}
                />
              </Col>
              <Col span={12}>
                <div style={{ marginBottom: 8 }}>
                  <Typography.Text>Wildcard Matching</Typography.Text>
                </div>
                <Switch
                  checked={pr.wildcard ?? false}
                  onChange={(checked) => onUpdate(idx, "wildcard", checked)}
                  style={{ marginRight: 8 }}
                />
                <Typography.Text type="secondary">
                  Prefix match + capture path (301/302 only)
                </Typography.Text>
              </Col>
            </Row>
          </>
        )}
      </Card>
    </div>
  );
};

export const DomainRedirectsButton = ({
  domain,
}: {
  domain: DomainRedirectsTarget;
}) => {
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [wholeToggle, setWholeToggle] = useState(!!domain.redirect_all_to);
  const [wholeUrl, setWholeUrl] = useState(domain.redirect_all_to ?? "");
  const [wholeType, setWholeType] = useState(domain.redirect_all_type ?? "301");
  const [pageRedirects, setPageRedirects] = useState<PageRedirect[]>(
    domain.page_redirects ?? []
  );
  const [isSaving, setIsSaving] = useState(false);
  const [expandedCards, setExpandedCards] = useState<Set<number>>(new Set());
  const qc = useQueryClient();

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    })
  );

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event;

    if (over && active.id !== over.id) {
      const oldIndex = Number(active.id);
      const newIndex = Number(over.id);
      setPageRedirects(arrayMove(pageRedirects, oldIndex, newIndex));
    }
  };

  const handleOpenModal = () => {
    // Re-sync from prop in case the value was updated elsewhere
    setWholeToggle(!!domain.redirect_all_to);
    setWholeUrl(domain.redirect_all_to ?? "");
    setWholeType(domain.redirect_all_type ?? "301");
    setPageRedirects(domain.page_redirects ?? []);
    setIsModalOpen(true);
  };

  const handleCloseModal = () => {
    setIsModalOpen(false);
  };

  const addPageRedirect = () => {
    setPageRedirects([
      ...pageRedirects,
      { source: "", destination: "", type: "301", active: true, wildcard: false },
    ]);
  };

  const removePageRedirect = (idx: number) => {
    setPageRedirects(pageRedirects.filter((_, i) => i !== idx));
  };

  const updatePageRedirect = (
    idx: number,
    key: keyof PageRedirect,
    value: string | boolean
  ) => {
    const updated = [...pageRedirects];
    updated[idx] = { ...updated[idx], [key]: value };
    setPageRedirects(updated);
  };

  const handleSave = async () => {
    setIsSaving(true);
    try {
      const body: Record<string, unknown> = {};

      if (wholeToggle) {
        const url = wholeUrl.trim();
        if (!url) {
          notification.error({ message: "Enter a redirect URL" });
          setIsSaving(false);
          return;
        }
        body.redirect_all_to = url;
        body.redirect_all_type = wholeType;
      } else {
        body.redirect_all_to = null;
        body.redirect_all_type = null;
      }

      const clean = pageRedirects.map((pr) => ({
        source: pr.source.trim(),
        destination: pr.destination.trim(),
        type: pr.type,
        active: pr.active ?? true,
        wildcard: pr.wildcard ?? false,
      }));
      // Don't send half-filled rows — drop blank ones silently
      body.page_redirects = clean.filter((pr) => pr.source && pr.destination);

      await apiClient.patch(`/domains/${domain.id}`, body);
      notification.success({ message: "Redirects saved" });
      qc.invalidateQueries({ queryKey: ["list", "domains"] });
      qc.invalidateQueries({ queryKey: ["one", "domains", domain.id] });
      setIsModalOpen(false);
    } catch (err) {
      const e = err as {
        response?: { data?: { detail?: string } };
        message?: string;
      };
      notification.error({
        message: "Failed to save",
        description: e.response?.data?.detail ?? e.message ?? "Unknown error",
      });
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <>
      <Button
        type="text"
        icon={<SwapOutlined />}
        onClick={handleOpenModal}
      >
        Redirects
      </Button>

      <Modal
        title={`Redirects for ${domain.name}`}
        open={isModalOpen}
        onCancel={handleCloseModal}
        width={720}
        footer={[
          <Button key="cancel" onClick={handleCloseModal}>
            Cancel
          </Button>,
          <Button
            key="save"
            type="primary"
            icon={<CheckOutlined />}
            onClick={handleSave}
            loading={isSaving}
          >
            Save Redirects
          </Button>,
        ]}
      >
        <Typography.Paragraph type="secondary" style={{ marginBottom: 24 }}>
          Redirect this domain to another domain or set up page redirects.
        </Typography.Paragraph>

        {/* Section 1: Redirect Entire Domain */}
        <div style={{ marginBottom: 32 }}>
          <div style={{ marginBottom: 12 }}>
            <Switch
              checked={wholeToggle}
              onChange={setWholeToggle}
              style={{ marginRight: 8 }}
            />
            <Typography.Text strong>Redirect Entire Domain</Typography.Text>
          </div>
          <Typography.Text type="secondary" style={{ marginBottom: 12, display: "block" }}>
            Redirect all traffic from this domain to another domain
          </Typography.Text>

          {wholeToggle && (
            <>
              <Row gutter={16} style={{ marginBottom: 12 }}>
                <Col span={12}>
                  <div style={{ marginBottom: 8 }}>
                    <Typography.Text>
                      Redirect To <Typography.Text type="danger">*</Typography.Text>
                    </Typography.Text>
                  </div>
                  <Input
                    placeholder="https://newdomain.com"
                    value={wholeUrl}
                    onChange={(e) => setWholeUrl(e.target.value)}
                  />
                </Col>
                <Col span={12}>
                  <div style={{ marginBottom: 8 }}>
                    <Typography.Text>
                      Redirect Type <Typography.Text type="danger">*</Typography.Text>
                    </Typography.Text>
                  </div>
                  <Select
                    value={wholeType}
                    onChange={setWholeType}
                    options={REDIRECT_TYPE_OPTIONS}
                    style={{ width: "100%" }}
                  />
                </Col>
              </Row>
              <Typography.Text type="secondary">
                All requests to this domain will be redirected to this URL
              </Typography.Text>
            </>
          )}
        </div>

        {/* Section 2: Page Redirects */}
        <div
          style={{
            opacity: wholeToggle ? 0.5 : 1,
            pointerEvents: wholeToggle ? "none" : "auto",
          }}
        >
          <div style={{ marginBottom: 12 }}>
            <Typography.Text strong>Page Redirects</Typography.Text>
          </div>

          <div style={{ marginBottom: 16 }}>
            {pageRedirects.length > 0 && (
              <DndContext
                sensors={sensors}
                collisionDetection={closestCenter}
                onDragEnd={handleDragEnd}
              >
                <SortableContext
                  items={pageRedirects.map((_, idx) => idx)}
                  strategy={verticalListSortingStrategy}
                >
                  {pageRedirects.map((pr, idx) => (
                    <SortableCard
                      key={idx}
                      idx={idx}
                      pr={pr}
                      isExpanded={expandedCards.has(idx)}
                      onToggleExpanded={(i) => {
                        const newSet = new Set(expandedCards);
                        if (newSet.has(i)) {
                          newSet.delete(i);
                        } else {
                          newSet.add(i);
                        }
                        setExpandedCards(newSet);
                      }}
                      onRemove={removePageRedirect}
                      onUpdate={updatePageRedirect}
                    />
                  ))}
                </SortableContext>
              </DndContext>
            )}
          </div>

          <div style={{ textAlign: "center", marginBottom: 16 }}>
            <Button icon={<PlusOutlined />} onClick={addPageRedirect}>
              Add Page Redirect
            </Button>
          </div>

          <Typography.Text type="secondary">
            Redirect specific paths to other URLs. Drag to reorder, toggle active status, or enable wildcard matching.
          </Typography.Text>
        </div>
      </Modal>
    </>
  );
};
