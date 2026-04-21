// UserDeleteAction — row-level destructive action with a confirmation
// modal. Exposes a two-state destructive choice (metadata-only
// vs. purge-OS) because the second one is irreversible and the user
// needs to see the difference before committing.
import { useState } from "react";
import { Button, Modal, Checkbox, Space, message } from "antd";
import { DeleteOutlined } from "@ant-design/icons";
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
  const [purgeOS, setPurgeOS] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const qc = useQueryClient();

  const handleOpenModal = () => {
    setIsModalOpen(true);
  };

  const handleCancel = () => {
    setIsModalOpen(false);
    setPurgeOS(false);
  };

  const handleDelete = async () => {
    setIsLoading(true);
    try {
      const url = `/users/${encodeURIComponent(recordItemId)}${
        purgeOS ? "?purge=true" : ""
      }`;
      await apiClient.delete(url);

      message.success(
        purgeOS
          ? `User "${userEmail}" and OS account deleted`
          : `User "${userEmail}" deleted`
      );

      // Invalidate every ["list", "users", *] variant so admin tabs
      // and the parent badge counters all refetch after a delete.
      qc.invalidateQueries({ queryKey: ["list", "users"] });
      qc.invalidateQueries({ queryKey: ["one", "users", recordItemId] });

      setIsModalOpen(false);
      setPurgeOS(false);
    } catch (err: unknown) {
      const errMsg =
        err instanceof Error ? err.message : "Failed to delete user";
      message.error(errMsg);
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <>
      <Button
        danger
        type="link"
        icon={<DeleteOutlined />}
        // Generic "Delete" — see RowActions in UserList.tsx for why
        // the email is intentionally kept out of the accessible name.
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
            Delete {purgeOS ? "and purge files" : "user"}
          </Button>,
        ]}
      >
        <Space orientation="vertical" style={{ width: "100%" }}>
          <div>
            <p>
              <strong>Delete user:</strong> Removes the user record and all
              owned domains from the database. The OS account and home
              directory stay on disk.
            </p>
            <p>
              <strong>Delete and purge files:</strong> Removes the user record,
              all owned domains, AND deletes the OS account (
              <code>userdel --remove</code>) along with the <code>/home</code>{" "}
              directory. This is permanent and cannot be undone.
            </p>
          </div>
          <Checkbox
            checked={purgeOS}
            onChange={(e) => setPurgeOS(e.target.checked)}
          >
            Also delete OS account and /home/{userEmail.split("@")[0]} directory
            (destructive)
          </Checkbox>
        </Space>
      </Modal>
    </>
  );
};
