// PasswordInput — drop-in replacement for AntD's Input.Password that
// adds a "generate strong password" button. Designed to be used inside
// a Form.Item; it speaks the same event-based value/onChange contract
// the native Input.Password uses, so existing Form.Item bindings don't
// need any adjustment.
//
// Usage:
//
//   <Form.Item label="Password" name="password" rules={…}>
//     <PasswordInput autoComplete="new-password" />
//   </Form.Item>
//
// Do NOT use this on sign-in pages — the login flow is for typing an
// existing password, not generating a new one.

import { Button, Input, Space, Tooltip } from "antd";
import { ThunderboltOutlined } from "@ant-design/icons";
import type { ChangeEvent, ComponentProps } from "react";

import { generatePassword } from "./passwordGenerator";

type InputPasswordProps = ComponentProps<typeof Input.Password>;

interface PasswordInputProps extends Omit<InputPasswordProps, "suffix"> {
  /** Length passed to generatePassword. Default 16. */
  generatorLength?: number;
}

export function PasswordInput({
  value,
  onChange,
  generatorLength = 16,
  ...rest
}: PasswordInputProps) {
  const handleGenerate = () => {
    const pw = generatePassword(generatorLength);
    // Forward a minimal ChangeEvent-shaped object so Form.Item (which
    // calls the child's onChange with whatever the child emits) lands
    // the value in form state the same way native keystrokes do.
    onChange?.({
      target: { value: pw },
      currentTarget: { value: pw },
    } as ChangeEvent<HTMLInputElement>);
  };

  return (
    <Space.Compact style={{ width: "100%", display: "flex" }}>
      <Input.Password
        {...rest}
        value={value}
        onChange={onChange}
        style={{ flex: 1 }}
      />
      <Tooltip title={`Generate ${generatorLength}-character strong password`}>
        <Button
          type="default"
          onClick={handleGenerate}
          icon={<ThunderboltOutlined />}
          aria-label="Generate strong password"
        />
      </Tooltip>
    </Space.Compact>
  );
}
