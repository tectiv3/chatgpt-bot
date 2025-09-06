const { createApp } = Vue;

createApp({
    delimiters: ['[[', ']]'],
    data() {
        return {
            loading: true,
            sidebarOpen: false,
            
            // Current state
            currentThreadId: null,
            currentThread: null,
            messages: [],
            threads: [],
            models: [],
            roles: [],
            
            // UI state
            messageInput: '',
            sending: false,
            showSettings: false,
            showRoleManager: false,
            showRoleEditor: false,
            
            // Settings
            threadSettings: {
                model_name: 'gpt-4o',
                temperature: 1.0,
                role_id: null,
                stream: true,
                qa: false,
                voice: false,
                lang: 'en',
                master_prompt: '',
                context_limit: 4000
            },
            
            // Role editing
            editingRole: {
                id: null,
                name: '',
                prompt: ''
            }
        }
    },
    
    computed: {
        // Use computed property for message count instead of manual updates
        currentMessageCount() {
            return this.messages.length;
        },
        
        // Computed property for getting current role name
        currentRoleName() {
            if (!this.threadSettings.role_id) return '';
            const role = this.roles.find(r => r.id === this.threadSettings.role_id);
            return role ? role.name : 'Unknown Role';
        },
        
        // Computed property for sorted threads (most recent first)
        sortedThreads() {
            return [...this.threads].sort((a, b) => {
                const dateA = new Date(a.updated_at || a.created_at);
                const dateB = new Date(b.updated_at || b.created_at);
                return dateB - dateA; // Most recent first
            });
        },
        
        // Computed property to determine if message can be sent
        canSendMessage() {
            return !this.sending && 
                   this.messageInput.trim().length > 0 && 
                   this.currentThreadId !== null;
        },
        
        // Computed property for message count display in threads list
        messageCount() {
            return this.messages.length;
        }
    },
    
    watch: {
        // Watch message count and update thread accordingly
        currentMessageCount(newCount) {
            if (this.currentThread) {
                this.currentThread.message_count = newCount;
            }
        }
    },
    
    async mounted() {
        console.log('Vue app mounted, Telegram WebApp available:', !!window.Telegram?.WebApp);
        
        // Initialize Telegram Web App
        if (window.Telegram?.WebApp) {
            window.Telegram.WebApp.ready();
            console.log('Telegram WebApp initialized', {
                initData: window.Telegram.WebApp.initData,
                user: window.Telegram.WebApp.initDataUnsafe?.user
            });
        } else {
            console.warn('Telegram WebApp not available - running in development mode?');
        }
        
        // Adapt to Telegram theme
        this.adaptToTelegramTheme();
        
        // Load initial data
        await this.loadInitialData();
        
        this.loading = false;
        
        // Handle Telegram events
        if (window.Telegram?.WebApp) {
            window.Telegram.WebApp.onEvent('themeChanged', this.adaptToTelegramTheme);
        }
    },
    
    methods: {
        adaptToTelegramTheme() {
            const webApp = window.Telegram?.WebApp;
            if (webApp?.themeParams) {
                document.documentElement.style.setProperty('--tg-bg-color', webApp.themeParams.bg_color);
                document.documentElement.style.setProperty('--tg-text-color', webApp.themeParams.text_color);
                document.documentElement.style.setProperty('--tg-accent-text-color', webApp.themeParams.accent_text_color);
                document.documentElement.style.setProperty('--tg-secondary-bg-color', webApp.themeParams.secondary_bg_color);
            }
            
            // Set viewport height correctly
            if (webApp?.viewportHeight) {
                document.body.style.height = `${webApp.viewportHeight}px`;
            } else {
                // Fallback for development
                document.body.style.height = '100vh';
            }
        },
        
        async loadInitialData() {
            try {
                const [threadsResponse, modelsResponse, rolesResponse] = await Promise.all([
                    this.apiCall('/api/threads'),
                    this.apiCall('/api/models'),
                    this.apiCall('/api/roles')
                ]);
                
                console.log('API Responses:', { threadsResponse, modelsResponse, rolesResponse });
                
                this.threads = threadsResponse.threads || [];
                this.models = modelsResponse.models || [];
                this.roles = rolesResponse.roles || [];
                
                console.log('Loaded data:', {
                    threads: this.threads.length,
                    models: this.models.length,
                    roles: this.roles.length
                });
                
                // Auto-select most recent thread
                if (this.threads.length > 0) {
                    await this.selectThread(this.threads[0].id);
                }
                
            } catch (error) {
                console.error('Failed to load initial data:', error);
                this.showError('Failed to load data. Check console for details.');
            }
        },
        
        async apiCall(endpoint, options = {}) {
            const initData = window.Telegram?.WebApp?.initData || '';
            const defaultOptions = {
                headers: {
                    'Content-Type': 'application/json',
                    'Telegram-Init-Data': initData
                }
            };
            
            console.log(`Making API call to ${endpoint}`, { 
                options, 
                initData: initData ? '[PRESENT]' : '[MISSING]',
                initDataLength: initData.length 
            });
            
            const response = await fetch(endpoint, { ...defaultOptions, ...options });
            
            console.log(`API response for ${endpoint}:`, { status: response.status, statusText: response.statusText });
            
            if (!response.ok) {
                const errorText = await response.text();
                console.error(`API call failed for ${endpoint}:`, { status: response.status, error: errorText });
                throw new Error(`API call failed: ${response.status} - ${errorText}`);
            }
            
            const data = await response.json();
            console.log(`API data for ${endpoint}:`, data);
            return data;
        },
        
        newThread() {
            console.log('🆕 Creating new thread locally...');
            
            // Reset thread settings to defaults
            this.threadSettings = {
                model_name: 'gpt-4o',
                temperature: 1.0,
                role_id: null,
                stream: true,
                qa: false,
                voice: false,
                lang: 'en',
                master_prompt: '',
                context_limit: 4000
            };
            
            // Create a temporary local thread
            const tempThreadId = 'temp_' + Date.now();
            const newThread = {
                id: tempThreadId,
                title: 'New Conversation',
                message_count: 0,
                created_at: new Date().toISOString(),
                updated_at: new Date().toISOString(),
                settings: { ...this.threadSettings }
            };
            
            // Add to threads list at the top
            this.threads.unshift(newThread);
            
            // Select this thread
            this.currentThreadId = tempThreadId;
            this.currentThread = newThread;
            this.messages = [];
            
            console.log('✅ Local thread created:', tempThreadId);
            
            // Focus the message input
            this.$nextTick(() => {
                const input = this.$refs.messageInput;
                if (input) input.focus();
            });
        },
        
        async selectThread(threadId) {
            if (this.currentThreadId === threadId) return;
            
            this.currentThreadId = threadId;
            this.currentThread = this.threads.find(t => t.id === threadId);
            this.sidebarOpen = false; // Close sidebar on mobile
            
            if (this.currentThread) {
                this.threadSettings = { ...this.currentThread.settings };
                await this.loadMessages();
            }
        },
        
        async loadThreads() {
            try {
                const response = await this.apiCall('/api/threads');
                this.threads = response.threads || [];
            } catch (error) {
                console.error('Failed to load threads:', error);
            }
        },
        
        async loadMessages() {
            if (!this.currentThreadId) return;
            
            // Skip loading for temp threads
            if (this.currentThreadId.startsWith('temp_')) {
                console.log('⏭️ Skipping message load for temp thread');
                return;
            }
            
            try {
                console.log('📥 Loading messages for thread:', this.currentThreadId);
                const response = await this.apiCall(`/api/threads/${this.currentThreadId}/messages`);
                const newMessages = response.messages || [];
                
                console.log('✅ Loaded messages:', newMessages.length);
                
                // Direct assignment for reactivity
                this.messages = newMessages;
                
                this.$nextTick(() => {
                    this.scrollToBottom();
                });
                
            } catch (error) {
                console.error('Failed to load messages:', error);
                this.showError('Failed to load messages');
            }
        },
        
        async sendMessage() {
            if (!this.messageInput.trim() || this.sending || !this.currentThreadId) return;
            
            const message = this.messageInput.trim();
            this.messageInput = '';
            this.sending = true;
            
            // Add user message to UI immediately
            this.messages.push({
                id: Date.now(),
                role: 'user',
                content: message,
                created_at: new Date().toISOString(),
                is_live: true,
                message_type: 'normal'
            });
            
            this.$nextTick(() => {
                this.scrollToBottom();
            });
            
            try {
                let apiThreadId = this.currentThreadId;
                
                // If this is a temporary thread, create it on the backend first
                if (this.currentThreadId.startsWith('temp_')) {
                    console.log('🔄 Converting temp thread to real thread...');
                    console.log('Current threadSettings before conversion:', this.threadSettings);
                    
                    const createResponse = await this.apiCall('/api/threads', {
                        method: 'POST',
                        body: JSON.stringify({
                            initial_message: message,
                            settings: this.threadSettings  // Use current settings from dropdowns
                        })
                    });
                    
                    apiThreadId = createResponse.thread_id;
                    
                    // Update the local thread with real ID and preserve settings
                    const threadIndex = this.threads.findIndex(t => t.id === this.currentThreadId);
                    if (threadIndex !== -1) {
                        this.threads[threadIndex].id = apiThreadId;
                        this.threads[threadIndex].title = message.substring(0, 50) + (message.length > 50 ? '...' : '');
                        this.threads[threadIndex].settings = { ...this.threadSettings }; // Preserve current settings
                    }
                    
                    this.currentThreadId = apiThreadId;
                    this.currentThread.id = apiThreadId;
                    // DON'T overwrite settings here - keep what user selected
                    
                    console.log('✅ Thread converted to real ID:', apiThreadId);
                    console.log('Settings preserved:', this.threadSettings);
                } else {
                    // Regular message to existing thread
                    await this.apiCall(`/api/threads/${apiThreadId}/chat`, {
                        method: 'POST',
                        body: JSON.stringify({ message })
                    });
                }
                
                // Reload messages to get AI response - with retries
                this.reloadMessagesWithRetry();
                
            } catch (error) {
                console.error('Failed to send message:', error);
                this.showError('Failed to send message');
            } finally {
                this.sending = false;
            }
        },
        
        async reloadMessagesWithRetry(attempt = 1, maxAttempts = 5) {
            console.log(`🔄 Reloading messages attempt ${attempt}/${maxAttempts}`);
            
            // Wait a bit before each attempt
            await new Promise(resolve => setTimeout(resolve, attempt * 500));
            
            const initialMessageCount = this.messages.length;
            await this.loadMessages();
            
            // Check if we got new messages
            if (this.messages.length > initialMessageCount) {
                console.log('✅ New messages detected, stopping retry');
                return;
            }
            
            // Retry if we haven't reached max attempts
            if (attempt < maxAttempts) {
                console.log(`⏳ No new messages yet, retrying in ${(attempt + 1) * 500}ms...`);
                this.reloadMessagesWithRetry(attempt + 1, maxAttempts);
            } else {
                console.log('❌ Max retry attempts reached');
            }
        },
        
        handleKeyDown(event) {
            if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault();
                this.sendMessage();
            }
        },
        
        // Update current thread settings when dropdowns change (for temp threads)
        updateThreadSettings() {
            if (this.currentThread) {
                this.currentThread.settings = { ...this.threadSettings };
                console.log('Thread settings updated locally:', this.threadSettings);
            }
        },
        
        async saveSettings() {
            if (!this.currentThreadId) return;
            
            // Don't save for temp threads - they'll be saved with first message
            if (this.currentThreadId.startsWith('temp_')) {
                this.updateThreadSettings();
                return;
            }
            
            try {
                await this.apiCall(`/api/threads/${this.currentThreadId}/settings`, {
                    method: 'PUT',
                    body: JSON.stringify(this.threadSettings)
                });
                
                // Update current thread settings
                if (this.currentThread) {
                    this.currentThread.settings = { ...this.threadSettings };
                }
                
                this.showSettings = false;
                
            } catch (error) {
                console.error('Failed to save settings:', error);
                this.showError('Failed to save settings');
            }
        },
        
        newRole() {
            this.editingRole = {
                id: null,
                name: '',
                prompt: ''
            };
            this.showRoleEditor = true;
        },
        
        editRole(role) {
            this.editingRole = { ...role };
            this.showRoleEditor = true;
        },
        
        async saveRole() {
            if (!this.editingRole.name.trim() || !this.editingRole.prompt.trim()) {
                this.showError('Please fill in both name and prompt');
                return;
            }
            
            try {
                if (this.editingRole.id) {
                    // Update existing role
                    await this.apiCall(`/api/roles/${this.editingRole.id}`, {
                        method: 'PUT',
                        body: JSON.stringify({
                            name: this.editingRole.name,
                            prompt: this.editingRole.prompt
                        })
                    });
                } else {
                    // Create new role
                    await this.apiCall('/api/roles', {
                        method: 'POST',
                        body: JSON.stringify({
                            name: this.editingRole.name,
                            prompt: this.editingRole.prompt
                        })
                    });
                }
                
                // Reload roles
                const response = await this.apiCall('/api/roles');
                console.log('Reloaded roles response:', response);
                this.roles = response.roles || [];
                console.log('Updated roles array:', this.roles);
                
                this.showRoleEditor = false;
                
            } catch (error) {
                console.error('Failed to save role:', error);
                this.showError('Failed to save role');
            }
        },
        
        async deleteRole(roleId) {
            if (!confirm('Are you sure you want to delete this role?')) return;
            
            try {
                await this.apiCall(`/api/roles/${roleId}`, {
                    method: 'DELETE'
                });
                
                // Remove from local array
                this.roles = this.roles.filter(r => r.id !== roleId);
                
                // If this role was selected in current thread, clear it
                if (this.threadSettings.role_id === roleId) {
                    this.threadSettings.role_id = null;
                    await this.saveSettings();
                }
                
            } catch (error) {
                console.error('Failed to delete role:', error);
                this.showError('Failed to delete role');
            }
        },
        
        getCurrentRoleName() {
            if (!this.currentThread?.settings?.role_id) return '';
            const role = this.roles.find(r => r.id === this.currentThread.settings.role_id);
            return role ? role.name : 'Unknown Role';
        },
        
        formatDate(dateStr) {
            const date = new Date(dateStr);
            const now = new Date();
            const diffMs = now - date;
            const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));
            
            if (diffDays === 0) {
                return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
            } else if (diffDays === 1) {
                return 'Yesterday';
            } else if (diffDays < 7) {
                return `${diffDays} days ago`;
            } else {
                return date.toLocaleDateString();
            }
        },
        
        formatTime(dateStr) {
            return new Date(dateStr).toLocaleTimeString([], { 
                hour: '2-digit', 
                minute: '2-digit' 
            });
        },
        
        formatMessage(content) {
            if (!content) return '';
            
            // Simple markdown-like formatting
            return content
                .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
                .replace(/\*(.*?)\*/g, '<em>$1</em>')
                .replace(/`(.*?)`/g, '<code>$1</code>')
                .replace(/\n/g, '<br>');
        },
        
        scrollToBottom() {
            const container = this.$refs.messagesContainer;
            if (container) {
                container.scrollTop = container.scrollHeight;
            }
        },
        
        showError(message) {
            // You can integrate with Telegram's showAlert or implement your own notification system
            if (window.Telegram?.WebApp?.showAlert) {
                window.Telegram.WebApp.showAlert(message);
            } else {
                alert(message);
            }
        }
    }
}).mount('#app');

// Global error handlers
window.addEventListener('error', function(e) {
    console.error('🚨 JavaScript Error:', e.error);
    console.error('📍 Error details:', {
        message: e.message,
        filename: e.filename,
        lineno: e.lineno,
        colno: e.colno,
        stack: e.error?.stack
    });
});

window.addEventListener('unhandledrejection', function(e) {
    console.error('🚨 Unhandled Promise Rejection:', e.reason);
    console.error('📍 Promise rejection details:', e);
});