const BASE_API_URL =
  process.env.NEXT_PUBLIC_FINALECHAT_API_URL || "http://127.0.0.1:7025";

module.exports = {
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${BASE_API_URL}/:path*`,
      },
    ];
  },
};
