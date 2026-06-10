# Stage 1: Build the React application
FROM node:20-alpine AS builder

WORKDIR /app

# Copy package.json and package-lock.json
COPY package*.json ./

# Install dependencies
RUN npm ci

# Copy the rest of the application code
COPY . .

# Build the application
RUN npm run build

# Stage 2: Serve the application using Nginx
FROM nginx:alpine

# Copy the build output to Nginx's html directory
COPY --from=builder /app/dist /usr/share/nginx/html

# Copy the Nginx template
COPY nginx.conf.template /etc/nginx/templates/default.conf.template

# Expose port 8080 (Scaleway Serverless standard)
EXPOSE 8080

# Nginx alpine image will automatically run envsubst on files in /etc/nginx/templates/ 
# and output them to /etc/nginx/conf.d/ before starting if placed as a .template.
# We just need to make sure the BACKEND_URL environment variable is provided at runtime.
# Default backend URL just in case (fallback)
ENV BACKEND_URL="http://localhost:8080"

CMD ["nginx", "-g", "daemon off;"]
