"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { useCallback } from "react";

import { Chat } from "../../types";

interface ChatListItemProps {
  chat: Chat;
  onSelect: () => void;
}

const ChatListItem = ({ chat, onSelect }: ChatListItemProps) => {
  const handleClick = useCallback(
    (e) => {
      e.preventDefault();
      onSelect();
    },
    [onSelect]
  );
  return (
    <li className="list-group-item" onClick={handleClick}>
      <button className="btn" onClick={handleClick}>
        {chat.summary}
      </button>
    </li>
  );
};

export default ChatListItem;
