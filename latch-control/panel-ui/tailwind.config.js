/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "media",
  theme: {
    extend: {
      colors: {
        brand: { DEFAULT: "#2563eb", fg: "#ffffff" },
      },
    },
  },
  plugins: [],
};
