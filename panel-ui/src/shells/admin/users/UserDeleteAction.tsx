// UserDeleteAction — row-level destructive action with a confirmation
// modal. There is no longer a "preserve files" mode; deleting a user
// always removes everything they own (domains, databases, mailboxes,
// cron jobs, OS account, /home, related rows).
import { useState } from "react";
import { Button, Modal, message } from "antd";
import { DeleteOutlined } from "@icons";
import { useQueryClient } from "@tanstack/react-query";

import { apiClient } from "../../../apiClient";

interface UserDeleteActionProps {
  recordItemId: string;
  userEmail: string;
}

export const UserDeleteAction = ({
  recordItemId,
  userEmail,
}: UserDeleteActionProps) => {
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const qc = useQueryClient();

  const handleOpenModal = () => {
    setIsModalOpen(true);
  };

  const handleCancel = () => {
    setIsModalOpen(false);
  };

  const handleDelete = async () => {
    setIsLoading(true);
    try {
      await apiClient.delete(`/users/${encodeURIComponent(recordItemId)}`);

      message.success(`User "${userEmail}" and all related data deleted`);

      // Invalidate every ["list", "users", *] variant so admin tabs
      // and the parent badge counters all refetch after a delete.
      qc.invalidateQueries({ queryKey: ["list", "users"] });
      qc.invalidateQueries({ queryKey: ["one", "users", recordItemId] });

      setIsModalOpen(false);
    } catch (err: unknown) {
      const errMsg =
        err instanceof Error ? err.message : "Failed to delete user";
      message.error(errMsg);
    } finally {
      setIsLoading(false);
    }
  };

  const localPart = userEmail.split("@")[0];

  return (
    <>
      <Button
        danger
        type="text"
        icon={<DeleteOutlined />}
        aria-label="Delete"
        onClick={handleOpenModal}
      />

      <Modal
        title={`Delete user "${userEmail}"?`}
        open={isModalOpen}
        onCancel={handleCancel}
        footer={[
          <Button key="cancel" onClick={handleCancel}>
            Cancel
          </Button>,
          <Button
            key="delete"
            danger
            type="primary"
            loading={isLoading}
            onClick={handleDelete}
          >
            Delete user
          </Button>,
        ]}
      >
        <p>
          This is permanent and cannot be undone. Deleting{" "}
          <strong>{userEmail}</strong> will remove:
        </p>
        <ul>
          <li>All owned domains, DNS zones, SSL certificates, nginx sites</li>
          <li>All databases and database users (panel + MariaDB)</li>
          <li>All mailboxes, forwarders, and Stalwart mail accounts</li>
          <li>All cron jobs, applications, SSH keys</li>
          <li>
            The OS account <code>{localPart}</code> and <code>/home/{localPart}</code>
          </li>
          <li>The Kratos identity (login record)</li>
        </ul>
      </Modal>
    </>
  );
};
