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
    <li className="list-group-item">
      <a href="#" onClick={handleClick}>
        {chat.summary}
      </a>
    </li>
  );
};

export default ChatListItem;
