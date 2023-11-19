export function toIsoString(date: Date): string {
  // Helper function to pad numbers with leading zeros
  const pad = (number: number, length: number = 2): string => {
    return number.toString().padStart(length, "0");
  };

  // Helper function to format timezone offset
  const formatTimezoneOffset = (offset: number): string => {
    const sign = offset > 0 ? "-" : "+"; // Inverting sign to match ISO-8601 format
    const absOffset = Math.abs(offset);
    const hours = pad(Math.floor(absOffset / 60));
    const minutes = pad(absOffset % 60);
    return `${sign}${hours}:${minutes}`;
  };

  // Extracting date and time components
  const year = date.getFullYear();
  const month = pad(date.getMonth() + 1);
  const day = pad(date.getDate());
  const hours = pad(date.getHours());
  const minutes = pad(date.getMinutes());
  const seconds = pad(date.getSeconds());
  const milliseconds = pad(date.getMilliseconds(), 3);

  // Timezone handling
  const timezoneOffset = date.getTimezoneOffset();
  const timezoneString =
    timezoneOffset !== 0 ? formatTimezoneOffset(timezoneOffset) : "Z";

  return `${year}-${month}-${day}T${hours}:${minutes}:${seconds}.${milliseconds}${timezoneString}`;
}
