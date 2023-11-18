"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import { Chat } from "./side_panel";
import ChatListItem from "./chat_list_item";

interface ChatListProps {
  chats: Chat[];
  isLoading: boolean;
}

const ChatList = ({ chats, isLoading }: ChatListProps) => (
  <div className="flex-grow-1 overflow-auto m-2">
    <ul className="list-group">
      {chats.map((chat) => (
        <ChatListItem key={chat.id} chat={chat} />
      ))}
    </ul>
    {isLoading && <div>Loading more chats...</div>}
  </div>
);

export default ChatList;
