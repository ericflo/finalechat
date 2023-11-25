import { useEffect, useRef } from "react";

export const useAutoResizeTextarea = (content) => {
  const textareaRef = useRef(null);

  useEffect(() => {
    const textarea = textareaRef.current;
    if (textarea) {
      textarea.style.height = "auto"; // Reset height to recalculate
      textarea.style.height = `${textarea.scrollHeight}px`; // Set to scroll height
    }
  }, [content]); // Depend on content to recalculate on change

  return textareaRef;
};
