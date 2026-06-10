/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        cyber: {
          dark: '#0f0f11',
          primary: '#00ffcc',
          secondary: '#ff00ff',
          accent: '#ff3333'
        }
      }
    },
  },
  plugins: [],
}
