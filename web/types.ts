export interface Chat {
  id: number;
  summary: string;
  created_timestamp: string;
  messages?: Message[];
}

export interface Message {
  id: number;
  chat_id: number;
  text: string;
  sender_type: string;
  timestamp: string;
}

export interface PaginatedResponse<T> {
  items: T[];
  total: number;
  pages: number;
  page: number;
}
