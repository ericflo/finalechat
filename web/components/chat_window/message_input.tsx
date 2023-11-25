"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useState, useCallback, useEffect } from "react";
import { useAutoResizeTextarea } from "../../hooks/textareas";

export interface MessageInputProps {
  chatId: number;
  onSendMessage: (message: string) => void;
}

const MessageInput: React.FC<MessageInputProps> = ({
  chatId,
  onSendMessage,
}) => {
  const [newMessage, setNewMessage] = useState<string>("");

  const handleTextareaChange = useCallback(
    (event: React.ChangeEvent<HTMLTextAreaElement>) => {
      setNewMessage(event.target.value);
    },
    []
  );

  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        if (newMessage.trim()) {
          onSendMessage(newMessage);
          setNewMessage("");
        }
      }
    },
    [newMessage, onSendMessage]
  );

  const handleSendClick = useCallback(() => {
    if (newMessage.trim()) {
      onSendMessage(newMessage);
      setNewMessage("");
    }
  }, [newMessage, onSendMessage]);

  // Clear the input when the chatId changes
  useEffect(() => {
    setNewMessage("");
  }, [chatId]);

  const textareaRef = useAutoResizeTextarea(newMessage);

  return (
    <div className="mt-auto pt-1 pe-2 pb-2 ps-4">
      <div className="input-group">
        <textarea
          ref={textareaRef}
          className="form-control"
          placeholder="Type a message..."
          value={newMessage}
          onChange={handleTextareaChange}
          onKeyDown={handleKeyDown}
          style={{ height: "50px", resize: "none" }}
        />
        <button className="btn btn-primary" onClick={handleSendClick}>
          Send
        </button>
      </div>
    </div>
  );
};

export default MessageInput;
