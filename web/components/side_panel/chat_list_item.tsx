"use client";

import { useCallback, useState } from "react";

import { Chat } from "../../types";

interface ChatListItemProps {
  chat: Chat;
  onSelect: () => void;
  onDelete: () => void;
}

const ChatListItem = ({ chat, onSelect, onDelete }: ChatListItemProps) => {
  const [showDelete, setShowDelete] = useState(false);

  const handleClick = useCallback(
    (e) => {
      e.preventDefault();
      onSelect();
    },
    [onSelect]
  );

  const handleDelete = useCallback(
    (e) => {
      e.stopPropagation();
      onDelete();
    },
    [onDelete]
  );

  return (
    <li
      className="list-group-item p-0 d-flex justify-content-between align-items-center"
      onMouseEnter={() => setShowDelete(true)}
      onMouseLeave={() => setShowDelete(false)}
    >
      <div className="me-2 text-nowrap overflow-hidden" onClick={handleClick}>
        <button className="btn" onClick={handleClick}>
          {chat.summary}
        </button>
      </div>
      {showDelete && (
        <button
          className="btn btn-sm"
          onClick={handleDelete}
          style={{ transition: "opacity 0.3s ease" }}
        >
          <i className="bi bi-trash-fill"></i>{" "}
          {/* Bootstrap icon for trash/delete */}
        </button>
      )}
    </li>
  );
};

export default ChatListItem;
