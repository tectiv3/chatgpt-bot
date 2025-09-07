const { createApp, ref, computed, watch, onMounted, onUnmounted, nextTick } = Vue

// DeviceStorage composable for Telegram WebApp persistent storage
const useDeviceStorage = () => {
    const isAvailable = computed(() => {
        return (
            window.Telegram?.WebApp?.CloudStorage ||
            window.Telegram?.WebApp?.initDataUnsafe?.user
        )
    })

    // Store data with fallback to localStorage
    const setItem = async (key, value) => {
        try {
            const serializedValue = JSON.stringify(value)

            if (window.Telegram?.WebApp?.CloudStorage) {
                // Use Telegram's CloudStorage API
                return new Promise((resolve, reject) => {
                    window.Telegram.WebApp.CloudStorage.setItem(
                        key,
                        serializedValue,
                        (error, result) => {
                            if (error) {
                                reject(new Error(error))
                            } else {
                                resolve(result)
                            }
                        }
                    )
                })
            } else {
                // Fallback to localStorage
                localStorage.setItem(`tg_miniapp_${key}`, serializedValue)
                return Promise.resolve(true)
            }
        } catch (error) {
            throw error
        }
    }

    // Get data with fallback to localStorage
    const getItem = async (key, defaultValue = null) => {
        try {
            if (window.Telegram?.WebApp?.CloudStorage) {
                // Use Telegram's CloudStorage API
                return new Promise(resolve => {
                    window.Telegram.WebApp.CloudStorage.getItem(key, (error, value) => {
                        if (error) {
                            resolve(defaultValue)
                        } else {
                            try {
                                const parsedValue = value ? JSON.parse(value) : defaultValue
                                resolve(parsedValue)
                            } catch (parseError) {
                                resolve(defaultValue)
                            }
                        }
                    })
                })
            } else {
                // Fallback to localStorage
                const stored = localStorage.getItem(`tg_miniapp_${key}`)
                if (stored) {
                    try {
                        return Promise.resolve(JSON.parse(stored))
                    } catch (error) {
                        return Promise.resolve(defaultValue)
                    }
                }
                return Promise.resolve(defaultValue)
            }
        } catch (error) {
            return Promise.resolve(defaultValue)
        }
    }

    // Remove item with fallback to localStorage
    const removeItem = async key => {
        try {
            if (window.Telegram?.WebApp?.CloudStorage) {
                return new Promise((resolve, reject) => {
                    window.Telegram.WebApp.CloudStorage.removeItem(key, (error, result) => {
                        if (error) {
                            reject(new Error(error))
                        } else {
                            resolve(result)
                        }
                    })
                })
            } else {
                // Fallback to localStorage
                localStorage.removeItem(`tg_miniapp_${key}`)
                return Promise.resolve(true)
            }
        } catch (error) {
            throw error
        }
    }

    return {
        isAvailable,
        setItem,
        getItem,
        removeItem,
    }
}

// Simplified mobile detection
const useMobileKeyboard = () => {
    const isMobile = computed(() => {
        return /Android|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(navigator.userAgent) || 'ontouchstart' in window
    })

    const focusInput = inputRef => {
        if (inputRef) {
            inputRef.focus()
        }
    }

    return {
        isMobile,
        focusInput
    }
}

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
            archivedThreads: [],
            models: [],
            roles: [],

            // UI state
            messageInput: '',
            sending: false,
            streaming: false,
            currentStreamController: null,

            // Streaming UX improvements
            streamingBuffer: '',
            streamingUpdateTimer: null,
            streamingThrottleMs: 50, // Throttle streaming updates to 20 FPS
            showSettings: false,
            showRoleManager: false,
            showRoleEditor: false,
            showDeleteConfirm: false,
            deleteThreadId: null,
            showArchivedSection: false,

            // Settings (will be loaded from DeviceStorage)
            threadSettings: {
                model_name: 'gpt-4o',
                temperature: 1.0,
                role_id: null,
                lang: 'en',
                master_prompt: '',
                context_limit: 4000,
            },

            // Persistent user preferences
            userPreferences: {
                selectedModel: 'gpt-4o',
                selectedRole: null,
                defaultTemperature: 1.0,
                enableStreaming: true,
            },

            // Role editing
            editingRole: {
                id: null,
                name: '',
                prompt: '',
            },

            // Markdown processor instance (initialized on first use)
            markdownProcessor: null,

            // Simple image attachment
            attachedImage: null,
        }
    },

    computed: {
        // Use computed property for message count instead of manual updates
        currentMessageCount() {
            return this.messages.length
        },

        // Computed property for getting current role name
        currentRoleName() {
            if (!this.threadSettings.role_id) return ''
            const role = this.roles.find(r => r.id === this.threadSettings.role_id)
            return role ? role.name : 'Unknown Role'
        },

        // Computed property for sorted threads (most recent first)
        sortedThreads() {
            return [...this.threads].sort((a, b) => {
                const dateA = new Date(a.updated_at || a.created_at)
                const dateB = new Date(b.updated_at || b.created_at)
                return dateB - dateA // Most recent first
            })
        },

        // Computed property to determine if message can be sent
        canSendMessage() {
            return (
                !this.sending &&
                !this.streaming &&
                this.messageInput.trim().length > 0 &&
                this.currentThreadId !== null
            )
        },

        // Computed property for send button state
        sendButtonState() {
            if (this.sending) return 'sending'
            if (this.streaming) return 'streaming'
            return 'idle'
        },

        // Computed property for send button icon
        sendButtonIcon() {
            switch (this.sendButtonState) {
                case 'sending':
                    return 'fas fa-spinner fa-spin'
                case 'streaming':
                    return 'fas fa-spinner fa-spin'
                default:
                    return 'fas fa-paper-plane'
            }
        },

        // Computed property for send button text
        sendButtonText() {
            switch (this.sendButtonState) {
                case 'sending':
                    return 'Sending...'
                case 'streaming':
                    return 'Streaming...'
                default:
                    return 'Send'
            }
        },

        // Check if streaming can be stopped
        canStopStreaming() {
            return this.streaming && this.currentStreamController
        },

        // Computed property for message count display in threads list
        messageCount() {
            return this.messages.length
        },

        // Check if there are archived threads to show section
        hasArchivedThreads() {
            return this.archivedThreads.length > 0
        },

        // Should show role selector (only for new threads)
        shouldShowRoleSelector() {
            return (
                !this.currentThreadId ||
                this.currentThreadId.startsWith('temp_') ||
                this.messages.length === 0
            )
        },
    },

    watch: {
        // Watch message count and update thread accordingly
        currentMessageCount(newCount) {
            if (this.currentThread) {
                this.currentThread.message_count = newCount
            }
        },
    },

    async mounted() {

        // Initialize composables
        this.deviceStorage = useDeviceStorage()
        this.mobileKeyboard = useMobileKeyboard()

        // Initialize Telegram Web App
        if (window.Telegram?.WebApp) {
            window.Telegram.WebApp.ready()
        }

        // Load user preferences from DeviceStorage before other data
        await this.loadUserPreferences()

        // Adapt to Telegram theme
        this.adaptToTelegramTheme()

        // Load initial data
        await this.loadInitialData()

        this.loading = false

        // Handle Telegram events
        if (window.Telegram?.WebApp) {
            window.Telegram.WebApp.onEvent('themeChanged', this.adaptToTelegramTheme)
        }

        // Add global keyboard event listener
        document.addEventListener('keydown', this.handleGlobalKeyDown)

        // Focus the message input if a thread is selected
        this.$nextTick(() => {
            this.focusInput()
        })
    },

    beforeUnmount() {
        // Clean up event listeners
        document.removeEventListener('keydown', this.handleGlobalKeyDown)

        // Clean up any active streaming and timers
        if (this.streamingUpdateTimer) {
            clearTimeout(this.streamingUpdateTimer)
        }
        this.stopStreaming()
    },

    methods: {
        adaptToTelegramTheme() {
            const webApp = window.Telegram?.WebApp
            if (webApp?.themeParams) {
                document.documentElement.style.setProperty(
                    '--tg-bg-color',
                    webApp.themeParams.bg_color
                )
                document.documentElement.style.setProperty(
                    '--tg-text-color',
                    webApp.themeParams.text_color
                )
                document.documentElement.style.setProperty(
                    '--tg-accent-text-color',
                    webApp.themeParams.accent_text_color
                )
                document.documentElement.style.setProperty(
                    '--tg-secondary-bg-color',
                    webApp.themeParams.secondary_bg_color
                )
            }

            // Set viewport height correctly
            if (webApp?.viewportHeight) {
                document.body.style.height = `${webApp.viewportHeight}px`
            } else {
                // Fallback for development
                document.body.style.height = '100vh'
            }
        },

        async loadInitialData() {
            try {
                const [threadsResponse, modelsResponse, rolesResponse, archivedResponse] =
                    await Promise.all([
                        this.apiCall('/api/threads'),
                        this.apiCall('/api/models'),
                        this.apiCall('/api/roles'),
                        this.apiCall('/api/threads/archived'),
                    ])

                this.threads = threadsResponse.threads || []
                this.models = modelsResponse.models || []
                this.roles = rolesResponse.roles || []
                this.archivedThreads = archivedResponse.threads || []

                // Auto-select most recent thread
                if (this.threads.length > 0) {
                    await this.selectThread(this.threads[0].id)
                }
            } catch (error) {
                this.showError('Failed to load data.')
            }
        },

        async apiCall(endpoint, options = {}) {
            const initData = window.Telegram?.WebApp?.initData || ''
            const defaultHeaders = {
                'Content-Type': 'application/json',
                'Telegram-Init-Data': initData,
            }


            const defaultOptions = {
                headers: defaultHeaders,
            }

            const response = await fetch(endpoint, { ...defaultOptions, ...options })

            if (!response.ok) {
                const errorText = await response.text()
                throw new Error(`API call failed: ${response.status} - ${errorText}`)
            }

            return await response.json()
        },

        newThread() {

            // Close sidebar
            this.sidebarOpen = false

            // Reset thread settings using user preferences as defaults
            this.threadSettings = {
                model_name: this.userPreferences.selectedModel || 'gpt-4o',
                temperature: this.userPreferences.defaultTemperature || 1.0,
                role_id: this.userPreferences.selectedRole || null,
                lang: 'en',
                master_prompt: '',
                context_limit: 4000,
            }

            // Create a temporary local thread
            const tempThreadId = 'temp_' + Date.now()
            const newThreadObj = {
                id: tempThreadId,
                title: 'New Conversation',
                message_count: 0,
                created_at: new Date().toISOString(),
                updated_at: new Date().toISOString(),
                settings: { ...this.threadSettings },
            }

            // Add to threads list at the top
            this.threads.unshift(newThreadObj)

            // Clear messages BEFORE setting thread to ensure reactivity
            this.messages = []
            
            // Select this thread
            this.currentThreadId = tempThreadId
            this.currentThread = newThreadObj

            // Force Vue to update the DOM with cleared messages
            this.$nextTick(() => {
                this.focusInput()
            })
        },

        async selectThread(threadId) {
            if (this.currentThreadId === threadId) return

            // Stop any active streaming when switching threads
            if (this.streaming) {
                this.stopStreaming()
            }

            this.currentThreadId = threadId
            this.currentThread =
                this.threads.find(t => t.id === threadId) ||
                this.archivedThreads.find(t => t.id === threadId)
            this.sidebarOpen = false // Close sidebar on mobile

            if (this.currentThread) {
                this.threadSettings = { ...this.currentThread.settings }
                await this.loadMessages()

                this.$nextTick(() => this.focusInput())
            }
        },

        async loadThreads() {
            try {
                const response = await this.apiCall('/api/threads')
                this.threads = response.threads || []
            } catch (error) {
                // Failed to load threads
            }
        },

        async loadMessages() {
            if (!this.currentThreadId) return

            // Skip loading for temp threads
            if (this.currentThreadId.startsWith('temp_')) {
                return
            }

            try {
                const response = await this.apiCall(
                    `/api/threads/${this.currentThreadId}/messages`
                )
                const newMessages = response.messages || []

                // Ensure all loaded messages have proper completion status
                // Messages from database without explicit is_complete should be marked as complete
                const processedMessages = newMessages.map(message => ({
                    ...message,
                    is_complete: message.is_complete !== false, // Default to true if not explicitly false
                }))

                // Direct assignment for reactivity
                this.messages = processedMessages

                // Use auto-scroll for long message history
                this.autoScrollToBottom()

                this.$nextTick(() => this.focusInput())
            } catch (error) {
                this.showError('Failed to load messages')
            }
        },

        async sendMessage() {
            if (
                (!this.messageInput.trim() && !this.attachedImage) ||
                this.sending ||
                !this.currentThreadId
            )
                return

            const message = this.messageInput.trim()
            const hasImage = !!this.attachedImage
            let attachedImageData = null

            // Store image data before clearing
            if (hasImage) {
                attachedImageData = {
                    preview: this.attachedImage.preview,
                    name: this.attachedImage.name,
                    file: this.attachedImage.file,
                }
            }

            const userMessage = {
                id: 'temp_' + Date.now(),
                role: 'user',
                content: message,
                created_at: new Date().toISOString(),
                is_live: true,
                message_type: hasImage ? 'image' : 'normal',
                image_data: hasImage ? attachedImageData.preview : null,
                image_name: hasImage ? attachedImageData.name : null,
                is_complete: true,
            }

            this.messages.push(userMessage)

            this.$nextTick(() => {
                this.scrollToBottom(true)
            })

            this.messageInput = ''
            this.attachedImage = null
            this.sending = true

            try {
                let apiThreadId = this.currentThreadId

                // If this is a temporary thread, create it on the backend first
                if (this.currentThreadId.startsWith('temp_')) {

                    const createResponse = await this.apiCall('/api/threads', {
                        method: 'POST',
                        body: JSON.stringify({
                            initial_message: message,
                            settings: this.threadSettings,
                        }),
                    })

                    apiThreadId = createResponse.thread_id

                    // Update the local thread with real ID
                    const threadIndex = this.threads.findIndex(
                        t => t.id === this.currentThreadId
                    )
                    if (threadIndex !== -1) {
                        this.threads[threadIndex].id = apiThreadId
                        this.threads[threadIndex].title =
                            message.substring(0, 50) + (message.length > 50 ? '...' : '')
                        this.threads[threadIndex].settings = { ...this.threadSettings }
                    }

                    // Update the current thread reference to point to the updated thread object
                    this.currentThreadId = apiThreadId
                    if (threadIndex !== -1) {
                        this.currentThread = this.threads[threadIndex]
                    }
                }

                // Prepare message payload with image data if present
                const messagePayload = {
                    message: message,
                }

                if (hasImage && attachedImageData) {
                    // Send single image data
                    messagePayload.image = {
                        data: attachedImageData.preview.split(',')[1], // Base64 data only
                        filename: attachedImageData.name,
                        mime_type: attachedImageData.file.type,
                    }
                }

                // Always use streaming for better UX
                await this.sendMessageWithStreaming(apiThreadId, messagePayload)
            } catch (error) {
                this.showError('Failed to send message')
            } finally {
                this.sending = false
                this.streaming = false

                // DON'T reload messages since we already show user message immediately
                // and backend will only send assistant response via streaming or sync response

                // Keep focus on mobile to prevent keyboard closing
                this.$nextTick(() => {
                    this.focusInput()
                })
            }
        },

        // Method to stop streaming
        stopStreaming() {

            if (this.currentStreamController) {
                this.currentStreamController.abort()
                this.currentStreamController = null
            }

            this.streaming = false
        },

        // Send message with Server-Sent Events streaming
        async sendMessageWithStreaming(threadId, messagePayload) {
            this.streaming = true
            this.currentStreamController = new AbortController()
            this.streamingBuffer = ''

            let streamingMessageId = null
            let lastUpdateTime = 0

            try {

                const headers = {
                    'Content-Type': 'application/json',
                    'Telegram-Init-Data': window.Telegram?.WebApp?.initData || '',
                }


                const response = await fetch(`/api/threads/${threadId}/messages`, {
                    method: 'POST',
                    headers: headers,
                    body: JSON.stringify(messagePayload),
                    signal: this.currentStreamController.signal,
                })

                if (!response.ok) {
                    throw new Error(`HTTP ${response.status}: ${response.statusText}`)
                }

                const reader = response.body.getReader()
                const decoder = new TextDecoder()

                while (true) {
                    const { done, value } = await reader.read()
                    if (done) break

                    const chunk = decoder.decode(value, { stream: true })
                    const lines = chunk.split('\n')

                    for (const line of lines) {
                        if (line.startsWith('data: ')) {
                            const jsonData = line.substring(6).trim()
                            if (jsonData) {
                                try {
                                    const data = JSON.parse(jsonData)

                                    if (data.type === 'complete') {
                                        this.streaming = false

                                        // Mark the streaming message as complete to stop indicator
                                        if (streamingMessageId) {
                                            const message = this.messages.find(
                                                m => m.id === streamingMessageId
                                            )
                                            if (message) {
                                                message.is_complete = true
                                                message.isStreaming = false
                                            }
                                        }

                                        return
                                    }

                                    // Handle streaming message updates
                                    if (
                                        data.role === 'assistant' &&
                                        data.content !== undefined
                                    ) {
                                        // Find or create the streaming message
                                        if (!streamingMessageId) {
                                            // First update - create assistant message
                                            const assistantMessage = {
                                                id: data.id,
                                                role: 'assistant',
                                                content: data.content || '',
                                                created_at: data.created_at || new Date().toISOString(),
                                                is_live: true,
                                                message_type: 'normal',
                                                is_complete: false,
                                                isStreaming: true,
                                            }
                                            this.messages.push(assistantMessage)
                                            streamingMessageId = data.id

                                            this.$nextTick(() => this.scrollToBottom(false))
                                        } else {
                                            // Update streaming content with throttling
                                            this.streamingBuffer = data.content || ''
                                            const now = Date.now()
                                            
                                            if (now - lastUpdateTime >= this.streamingThrottleMs) {
                                                this.updateStreamingMessage(streamingMessageId, this.streamingBuffer)
                                                lastUpdateTime = now
                                            } else {
                                                if (this.streamingUpdateTimer) {
                                                    clearTimeout(this.streamingUpdateTimer)
                                                }
                                                this.streamingUpdateTimer = setTimeout(() => {
                                                    this.updateStreamingMessage(streamingMessageId, this.streamingBuffer)
                                                    lastUpdateTime = Date.now()
                                                }, this.streamingThrottleMs - (now - lastUpdateTime))
                                            }
                                        }
                                    }
                                } catch (e) {
                                    // Error parsing streaming data
                                }
                            }
                        }
                    }
                }
            } catch (error) {

                // Handle cancellation gracefully
                if (
                    error.name === 'AbortError' ||
                    error.message.includes('aborted') ||
                    error.message.includes('canceled')
                ) {
                    // Add interruption message to the streaming response
                    if (streamingMessageId) {
                        const message = this.messages.find(m => m.id === streamingMessageId)
                        if (message && message.content) {
                            message.content += '\n\n_[Streaming was interrupted by user]_'
                            message.is_complete = true
                            message.isStreaming = false
                        }
                    }
                } else {
                    throw error
                }
            } finally {
                this.streaming = false
                this.currentStreamController = null

                // Clean up streaming state and ensure final update
                if (this.streamingUpdateTimer) {
                    clearTimeout(this.streamingUpdateTimer)
                    this.streamingUpdateTimer = null
                }

                if (streamingMessageId && this.streamingBuffer) {
                    // Final update with any remaining buffer content
                    this.updateStreamingMessage(streamingMessageId, this.streamingBuffer)
                }

                if (streamingMessageId) {
                    const message = this.messages.find(m => m.id === streamingMessageId)
                    if (message) {
                        message.isStreaming = false
                    }
                }

                this.streamingBuffer = ''
            }
        },

        // Remove all polling logic as we now use synchronous requests with SSE streaming

        handleKeyDown(event) {
            if (event.key === 'Enter') {
                if (this.mobileKeyboard.isMobile.value) {
                    // On mobile, only send on Shift+Enter
                    if (event.shiftKey) {
                        event.preventDefault()
                        this.sendMessage()
                    }
                } else {
                    // On desktop, Enter sends message, Shift+Enter creates new line
                    if (!event.shiftKey) {
                        event.preventDefault()
                        this.sendMessage()
                    }
                }
            }

            // Handle Escape key to stop streaming
            if (event.key === 'Escape' && this.streaming) {
                event.preventDefault()
                this.stopStreaming()
            }
        },

        // Global keyboard shortcuts
        handleGlobalKeyDown(event) {
            // Cmd+N (Mac) or Ctrl+N (Windows/Linux) to create new thread
            if ((event.metaKey || event.ctrlKey) && event.key === 'n') {
                event.preventDefault()
                this.newThread()
            }
        },

        // Update current thread settings when dropdowns change (for temp threads)
        updateThreadSettings() {
            if (this.currentThread) {
                this.currentThread.settings = { ...this.threadSettings }
            }

            // Save user preferences when model or role changes
            this.saveUserPreference('selectedModel', this.threadSettings.model_name)
            this.saveUserPreference('selectedRole', this.threadSettings.role_id)

            this.$nextTick(() => this.focusInput())
        },

        async saveSettings() {
            if (!this.currentThreadId) {
                return
            }

            // Don't save for temp threads - they'll be saved with first message
            if (this.currentThreadId.startsWith('temp_')) {
                this.updateThreadSettings()
                this.showSettings = false
                return
            }

            try {
                await this.apiCall(`/api/threads/${this.currentThreadId}/settings`, {
                    method: 'PUT',
                    body: JSON.stringify(this.threadSettings),
                })

                // Update current thread settings
                if (this.currentThread) {
                    this.currentThread.settings = { ...this.threadSettings }
                }

                // Save user preferences when settings are saved
                await this.saveUserPreferences()

                this.showSettings = false

                this.$nextTick(() => this.focusInput())
            } catch (error) {
                this.showError('Failed to save settings')
            }
        },

        newRole() {
            this.editingRole = {
                id: null,
                name: '',
                prompt: '',
            }
            this.showRoleEditor = true
        },

        editRole(role) {
            this.editingRole = { ...role }
            this.showRoleEditor = true
        },

        async saveRole() {
            if (!this.editingRole.name.trim() || !this.editingRole.prompt.trim()) {
                this.showError('Please fill in both name and prompt')
                return
            }

            try {
                if (this.editingRole.id) {
                    // Update existing role
                    await this.apiCall(`/api/roles/${this.editingRole.id}`, {
                        method: 'PUT',
                        body: JSON.stringify({
                            name: this.editingRole.name,
                            prompt: this.editingRole.prompt,
                        }),
                    })
                } else {
                    // Create new role
                    await this.apiCall('/api/roles', {
                        method: 'POST',
                        body: JSON.stringify({
                            name: this.editingRole.name,
                            prompt: this.editingRole.prompt,
                        }),
                    })
                }

                // Reload roles
                const response = await this.apiCall('/api/roles')
                this.roles = response.roles || []

                this.showRoleEditor = false

                this.$nextTick(() => this.focusInput())
            } catch (error) {
                this.showError('Failed to save role')
            }
        },

        async deleteRole(roleId) {
            if (!confirm('Are you sure you want to delete this role?')) return

            try {
                await this.apiCall(`/api/roles/${roleId}`, {
                    method: 'DELETE',
                })

                // Remove from local array
                this.roles = this.roles.filter(r => r.id !== roleId)

                // If this role was selected in current thread, clear it
                if (this.threadSettings.role_id === roleId) {
                    this.threadSettings.role_id = null
                    await this.saveSettings()
                }
            } catch (error) {
                this.showError('Failed to delete role')
            }
        },

        getCurrentRoleName() {
            if (!this.currentThread?.settings?.role_id) return ''
            const role = this.roles.find(r => r.id === this.currentThread.settings.role_id)
            return role ? role.name : 'Unknown Role'
        },

        formatDate(dateStr) {
            const date = new Date(dateStr)
            const now = new Date()
            const diffMs = now - date
            const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))

            if (diffDays === 0) {
                return date.toLocaleTimeString('en-GB', {
                    hour: '2-digit',
                    minute: '2-digit',
                    hour12: false,
                })
            } else if (diffDays === 1) {
                return 'Yesterday'
            } else if (diffDays < 7) {
                return `${diffDays} days ago`
            } else {
                return date.toLocaleDateString()
            }
        },

        formatTime(dateStr) {
            return new Date(dateStr).toLocaleTimeString('en-GB', {
                hour: '2-digit',
                minute: '2-digit',
                hour12: false,
            })
        },

        formatMessage(content) {
            if (!content) return ''
            
            if (!this.markdownProcessor) {
                this.markdownProcessor = window.markdownit({
                    html: false,
                    linkify: true,
                    typographer: true,
                    breaks: true
                })
            }
            
            return this.markdownProcessor.render(content)
        },

        showError(message) {
            // You can integrate with Telegram's showAlert or implement your own notification system
            if (window.Telegram?.WebApp?.showAlert) {
                window.Telegram.WebApp.showAlert(message)
            } else {
                alert(message)
            }
        },

        // Thread management functions
        confirmDeleteThread(threadId) {
            // For temp threads, delete immediately without confirmation
            if (threadId.toString().startsWith('temp_')) {
                this.deleteThreadId = threadId
                this.deleteThread()
                return
            }

            // For real threads, show confirmation dialog
            this.deleteThreadId = threadId
            this.showDeleteConfirm = true
        },

        cancelDelete() {
            this.showDeleteConfirm = false
            this.deleteThreadId = null
        },

        async deleteThread() {
            if (!this.deleteThreadId) return

            try {
                // Check if it's a temp thread
                const isTemp = this.deleteThreadId.toString().startsWith('temp_')

                if (!isTemp) {
                    // Only make API call for real threads
                    await this.apiCall(`/api/threads/${this.deleteThreadId}`, {
                        method: 'DELETE',
                    })
                }

                // Remove from local arrays
                this.threads = this.threads.filter(t => t.id !== this.deleteThreadId)
                this.archivedThreads = this.archivedThreads.filter(
                    t => t.id !== this.deleteThreadId
                )

                // If currently selected thread was deleted, clear selection
                if (this.currentThreadId === this.deleteThreadId) {
                    this.currentThreadId = null
                    this.currentThread = null
                    this.messages = []

                    // Auto-select next available thread
                    if (this.threads.length > 0) {
                        await this.selectThread(this.threads[0].id)
                    }
                }

                this.showDeleteConfirm = false
                this.deleteThreadId = null

                // Thread deleted
            } catch (error) {
                this.showError('Failed to delete thread')
            }
        },

        async archiveThread(threadId) {
            try {
                await this.apiCall(`/api/threads/${threadId}/archive`, {
                    method: 'PUT',
                })

                // Find the thread in either active or archived list
                let thread = this.threads.find(t => t.id === threadId)
                let wasActive = true

                if (!thread) {
                    thread = this.archivedThreads.find(t => t.id === threadId)
                    wasActive = false
                }

                if (thread) {
                    // Toggle archive status
                    if (wasActive) {
                        // Move from active to archived
                        this.threads = this.threads.filter(t => t.id !== threadId)
                        thread.archived_at = new Date().toISOString()
                        this.archivedThreads.unshift(thread)

                        // If currently selected thread was archived, clear selection and select next
                        if (this.currentThreadId === threadId) {
                            this.currentThreadId = null
                            this.currentThread = null
                            this.messages = []

                            if (this.threads.length > 0) {
                                await this.selectThread(this.threads[0].id)
                            }
                        }
                    } else {
                        // Move from archived to active
                        this.archivedThreads = this.archivedThreads.filter(
                            t => t.id !== threadId
                        )
                        thread.archived_at = null
                        this.threads.unshift(thread)
                    }
                }
            } catch (error) {
                this.showError('Failed to archive thread')
            }
        },

        toggleArchivedSection() {
            this.showArchivedSection = !this.showArchivedSection
        },

        scrollToBottom(smooth = true) {
            const container = this.$refs.messagesContainer
            if (container) {
                container.scrollTo({
                    top: container.scrollHeight,
                    behavior: smooth ? 'smooth' : 'auto'
                })
            }
        },

        autoScrollToBottom() {
            this.$nextTick(() => {
                setTimeout(() => this.scrollToBottom(false), 100)
            })
        },

        updateStreamingMessage(messageId, content) {
            const message = this.messages.find(m => m.id === messageId)
            if (message) {
                message.content = content
                this.$nextTick(() => {
                    const container = this.$refs.messagesContainer
                    if (container) {
                        const isNearBottom = container.scrollHeight - container.scrollTop - container.clientHeight < 100
                        if (isNearBottom) {
                            this.scrollToBottom(true)
                        }
                    }
                })
            }
        },



        // DeviceStorage integration methods
        async loadUserPreferences() {
            const [selectedModel, selectedRole, defaultTemperature, enableStreaming] =
                await Promise.all([
                    this.deviceStorage.getItem('selectedModel', 'gpt-4o'),
                    this.deviceStorage.getItem('selectedRole', null),
                    this.deviceStorage.getItem('defaultTemperature', 1.0),
                    this.deviceStorage.getItem('enableStreaming', true),
                ])

            this.userPreferences = {
                selectedModel,
                selectedRole,
                defaultTemperature,
                enableStreaming,
            }
        },


        async saveUserPreferences() {
            try {

                // Extract current preferences from thread settings
                this.userPreferences = {
                    selectedModel: this.threadSettings.model_name,
                    selectedRole: this.threadSettings.role_id,
                    defaultTemperature: this.threadSettings.temperature,
                    enableStreaming: this.threadSettings.stream,
                }

                await Promise.all([
                    this.deviceStorage.setItem(
                        'selectedModel',
                        this.userPreferences.selectedModel
                    ),
                    this.deviceStorage.setItem(
                        'selectedRole',
                        this.userPreferences.selectedRole
                    ),
                    this.deviceStorage.setItem(
                        'defaultTemperature',
                        this.userPreferences.defaultTemperature
                    ),
                    this.deviceStorage.setItem(
                        'enableStreaming',
                        this.userPreferences.enableStreaming
                    ),
                ])
            } catch (error) {
                // Failed to save user preferences
            }
        },

        async saveUserPreference(key, value) {
            try {
                this.userPreferences[key] = value
                await this.deviceStorage.setItem(key, value)
            } catch (error) {
                // Failed to save user preference
            }
        },


        focusInput() {
            const input = this.$refs.messageInput
            if (input) {
                this.mobileKeyboard.focusInput(input)
            }
        },

        onInputFocus() {
            // Input focused
        },

        onInputBlur() {
            // Input blurred
        },

        onInputTouch(event) {
            event.target.focus()
        },

        // Simple image attachment methods
        selectImage() {
            const input = document.createElement('input')
            input.type = 'file'
            input.accept = 'image/jpeg,image/jpg,image/png,image/gif,image/webp'
            input.addEventListener('change', event => this.handleImageSelect(event))
            input.click()
        },

        handleImageSelect(event) {
            const file = event.target.files[0]
            if (!file) return

            // Validate file
            if (!this.validateImageFile(file)) return

            // Create preview
            const reader = new FileReader()
            reader.onload = e => {
                this.attachedImage = {
                    file: file,
                    name: file.name,
                    size: file.size,
                    preview: e.target.result,
                }

                this.$nextTick(() => this.focusInput())
            }
            reader.readAsDataURL(file)
        },

        validateImageFile(file) {
            const allowedTypes = ['image/jpeg', 'image/jpg', 'image/png', 'image/gif', 'image/webp']
            if (!allowedTypes.includes(file.type)) {
                this.showError('File type not supported. Please use JPEG, PNG, GIF, or WebP.')
                return false
            }

            if (file.size > 10 * 1024 * 1024) {
                this.showError('File is too large. Maximum size is 10MB.')
                return false
            }

            return true
        },

        clearAttachedImage() {
            this.attachedImage = null
        },

        formatFileSize(bytes) {
            if (bytes === 0) return '0 B'
            const k = 1024
            const sizes = ['B', 'KB', 'MB', 'GB']
            const i = Math.floor(Math.log(bytes) / Math.log(k))
            return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
        },
    },
}).mount('#app')

