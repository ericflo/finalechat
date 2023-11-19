"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { Chat } from "./side_panel";

interface ChatListItemProps {
  chat: Chat;
}

const ChatListItem = ({ chat }: ChatListItemProps) => (
  <li className="list-group-item">
    {chat.title}
    {/* Add more chat details here */}
  </li>
);

export default ChatListItem;
