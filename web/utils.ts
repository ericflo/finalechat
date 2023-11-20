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

export function fromIsoString(isoString: string): Date {
  // Regular expression to parse the ISO-8601 date string
  const regex =
    /(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})\.(\d{3})([Z+-].*)?/;
  const match = isoString.match(regex);

  if (!match) {
    throw new Error("Invalid ISO-8601 date string");
  }

  // Extracting date and time components
  const [year, month, day, hour, minute, second, millisecond, timezone] =
    match.slice(1);

  // Creating the date object in UTC
  const date = new Date(
    Date.UTC(
      parseInt(year, 10),
      parseInt(month, 10) - 1, // Month is 0-indexed in JavaScript
      parseInt(day, 10),
      parseInt(hour, 10),
      parseInt(minute, 10),
      parseInt(second, 10),
      parseInt(millisecond, 10)
    )
  );

  // Adjusting for timezone if necessary
  if (timezone && timezone !== "Z") {
    const sign = timezone[0] === "+" ? -1 : 1; // Inverting the sign for adjustment
    const [tzHour, tzMinute] = timezone
      .substring(1)
      .split(":")
      .map((s) => parseInt(s, 10));
    date.setUTCMinutes(date.getUTCMinutes() + sign * (tzHour * 60 + tzMinute));
  }

  return date;
}
