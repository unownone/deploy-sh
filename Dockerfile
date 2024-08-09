# Use the Nginx base image
FROM nginx:alpine

# Create a simple HTML file with "Hello, World!"
RUN echo "<h1>Brought to you by Kaniko & Kubernetes</h1>" > /usr/share/nginx/html/index.html

# Expose port 80
EXPOSE 80

# Command to start Nginx when the container launches
CMD ["nginx", "-g", "daemon off;"]
