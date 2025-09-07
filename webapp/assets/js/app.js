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
                                console.error('DeviceStorage setItem error:', error)
                                reject(new Error(error))
                            } else {
                                console.log('DeviceStorage setItem success:', key, result)
                                resolve(result)
                            }
                        }
                    )
                })
            } else {
                // Fallback to localStorage
                console.warn('DeviceStorage not available, using localStorage fallback')
                localStorage.setItem(`tg_miniapp_${key}`, serializedValue)
                return Promise.resolve(true)
            }
        } catch (error) {
            console.error('DeviceStorage setItem failed:', key, error)
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
                            console.error('DeviceStorage getItem error:', error)
                            resolve(defaultValue)
                        } else {
                            try {
                                const parsedValue = value ? JSON.parse(value) : defaultValue
                                console.log('DeviceStorage getItem success:', key, parsedValue)
                                resolve(parsedValue)
                            } catch (parseError) {
                                console.error('DeviceStorage parse error:', parseError)
                                resolve(defaultValue)
                            }
                        }
                    })
                })
            } else {
                // Fallback to localStorage
                console.warn('DeviceStorage not available, using localStorage fallback')
                const stored = localStorage.getItem(`tg_miniapp_${key}`)
                if (stored) {
                    try {
                        return Promise.resolve(JSON.parse(stored))
                    } catch (error) {
                        console.error('localStorage parse error:', error)
                        return Promise.resolve(defaultValue)
                    }
                }
                return Promise.resolve(defaultValue)
            }
        } catch (error) {
            console.error('DeviceStorage getItem failed:', key, error)
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
                            console.error('DeviceStorage removeItem error:', error)
                            reject(new Error(error))
                        } else {
                            console.log('DeviceStorage removeItem success:', key)
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
            console.error('DeviceStorage removeItem failed:', key, error)
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

// Mobile keyboard management composable
const useMobileKeyboard = () => {
    const isMobile = computed(() => {
        return (
            /Android|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(
                navigator.userAgent
            ) ||
            'ontouchstart' in window ||
            window.matchMedia('(max-width: 768px)').matches ||
            window.Telegram?.WebApp?.platform === 'android' ||
            window.Telegram?.WebApp?.platform === 'ios'
        )
    })

    const maintainFocus = async inputRef => {
        if (!isMobile.value || !inputRef) return

        // Use multiple strategies to maintain focus on mobile
        await nextTick()

        // Strategy 1: Immediate focus
        inputRef.focus()

        // Strategy 2: Delayed focus to handle virtual keyboard animations
        setTimeout(() => {
            if (inputRef && document.activeElement !== inputRef) {
                inputRef.focus()
            }
        }, 100)

        // Strategy 3: Additional delayed focus for iOS
        if (window.Telegram?.WebApp?.platform === 'ios') {
            setTimeout(() => {
                if (inputRef && document.activeElement !== inputRef) {
                    inputRef.focus()
                    inputRef.click() // iOS sometimes needs a click event
                }
            }, 300)
        }
    }

    const preventKeyboardCollapse = inputRef => {
        if (!isMobile.value || !inputRef) return

        // Prevent blur events that might close the keyboard
        inputRef.addEventListener(
            'blur',
            event => {
                // Re-focus after a short delay if the blur wasn't intentional
                setTimeout(() => {
                    if (document.activeElement === document.body) {
                        inputRef.focus()
                    }
                }, 50)
            },
            { passive: true }
        )
    }

    return {
        isMobile,
        maintainFocus,
        preventKeyboardCollapse,
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
            showConnectionStatus: false,
            connectionStatus: 'connected',
            connectionStatusText: 'Connected',

            // Settings (will be loaded from DeviceStorage)
            threadSettings: {
                model_name: 'gpt-4o',
                temperature: 1.0,
                role_id: null,
                stream: true,
                qa: false,
                voice: false,
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

            // CSRF token for request security
            csrfToken: null,

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
        console.log('Vue app mounted, Telegram WebApp available:', !!window.Telegram?.WebApp)

        // Check library availability
        console.log('Library availability:', {
            markdownit: !!window.markdownit,
            DOMPurify: !!window.DOMPurify,
            Vue: !!window.Vue,
        })

        // Initialize DeviceStorage and Mobile Keyboard composables
        this.deviceStorage = useDeviceStorage()
        this.mobileKeyboard = useMobileKeyboard()

        // Initialize Telegram Web App
        if (window.Telegram?.WebApp) {
            window.Telegram.WebApp.ready()
            console.log('Telegram WebApp initialized', {
                initData: window.Telegram.WebApp.initData,
                user: window.Telegram.WebApp.initDataUnsafe?.user,
                platform: window.Telegram.WebApp.platform,
                version: window.Telegram.WebApp.version,
                cloudStorageAvailable: !!window.Telegram.WebApp.CloudStorage,
            })
        } else {
            console.warn('Telegram WebApp not available - running in development mode?')
        }

        // Load user preferences from DeviceStorage before other data
        await this.loadUserPreferences()

        // Load CSRF token for secure requests
        await this.loadCSRFToken()

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

        // Setup mobile keyboard management
        this.setupMobileKeyboardHandling()

        // Focus the message input if a thread is selected
        this.$nextTick(() => {
            const input = this.$refs.messageInput
            if (input && this.currentThreadId) {
                this.focusInput()
            }
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

                console.log('API Responses:', {
                    threadsResponse,
                    modelsResponse,
                    rolesResponse,
                    archivedResponse,
                })

                this.threads = threadsResponse.threads || []
                this.models = modelsResponse.models || []
                this.roles = rolesResponse.roles || []
                this.archivedThreads = archivedResponse.threads || []

                console.log('Loaded data:', {
                    threads: this.threads.length,
                    models: this.models.length,
                    roles: this.roles.length,
                    archived: this.archivedThreads.length,
                })

                // Auto-select most recent thread
                if (this.threads.length > 0) {
                    await this.selectThread(this.threads[0].id)
                }
            } catch (error) {
                console.error('Failed to load initial data:', error)
                this.showError('Failed to load data. Check console for details.')
            }
        },

        async apiCall(endpoint, options = {}) {
            const initData = window.Telegram?.WebApp?.initData || ''
            const defaultHeaders = {
                'Content-Type': 'application/json',
                'Telegram-Init-Data': initData,
            }

            // Add CSRF token for non-GET requests
            if (options.method && options.method !== 'GET' && this.csrfToken) {
                defaultHeaders['X-CSRF-Token'] = this.csrfToken
            }

            const defaultOptions = {
                headers: defaultHeaders,
            }

            console.log(`Making API call to ${endpoint}`, {
                options,
                initData: initData ? '[PRESENT]' : '[MISSING]',
                initDataLength: initData.length,
                hasCSRF: !!this.csrfToken,
            })

            const response = await fetch(endpoint, { ...defaultOptions, ...options })

            console.log(`API response for ${endpoint}:`, {
                status: response.status,
                statusText: response.statusText,
            })

            if (!response.ok) {
                const errorText = await response.text()
                console.error(`API call failed for ${endpoint}:`, {
                    status: response.status,
                    error: errorText,
                })
                throw new Error(`API call failed: ${response.status} - ${errorText}`)
            }

            const data = await response.json()
            console.log(`API data for ${endpoint}:`, data)
            return data
        },

        newThread() {
            console.log('🆕 Creating new thread locally...')

            // Close sidebar
            this.sidebarOpen = false

            // Reset thread settings using user preferences as defaults
            this.threadSettings = {
                model_name: this.userPreferences.selectedModel || 'gpt-4o',
                temperature: this.userPreferences.defaultTemperature || 1.0,
                role_id: this.userPreferences.selectedRole || null,
                stream: this.userPreferences.enableStreaming !== false,
                qa: false,
                voice: false,
                lang: 'en',
                master_prompt: '',
                context_limit: 4000,
            }

            // Create a temporary local thread
            const tempThreadId = 'temp_' + Date.now()
            const newThread = {
                id: tempThreadId,
                title: 'New Conversation',
                message_count: 0,
                created_at: new Date().toISOString(),
                updated_at: new Date().toISOString(),
                settings: { ...this.threadSettings },
            }

            // Add to threads list at the top
            this.threads.unshift(newThread)

            // Select this thread
            this.currentThreadId = tempThreadId
            this.currentThread = newThread
            this.messages = []

            console.log('✅ Local thread created:', tempThreadId)

            // Focus the message input with mobile-optimized handling
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
                this.threadSettings = { 
                    ...this.currentThread.settings,
                    // Ensure stream is enabled by default if not set
                    stream: this.currentThread.settings.stream !== false
                }
                await this.loadMessages()

                // Focus the message input after thread is selected
                this.$nextTick(() => {
                    this.focusInput()
                })
            }
        },

        async loadThreads() {
            try {
                const response = await this.apiCall('/api/threads')
                this.threads = response.threads || []
            } catch (error) {
                console.error('Failed to load threads:', error)
            }
        },

        async loadMessages() {
            if (!this.currentThreadId) return

            // Skip loading for temp threads
            if (this.currentThreadId.startsWith('temp_')) {
                console.log('⏭️ Skipping message load for temp thread')
                return
            }

            try {
                console.log('📥 Loading messages for thread:', this.currentThreadId)
                const response = await this.apiCall(
                    `/api/threads/${this.currentThreadId}/messages`
                )
                const newMessages = response.messages || []

                console.log('✅ Loaded messages:', newMessages.length)

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

                // Focus the message input after messages are loaded
                this.$nextTick(() => {
                    this.focusInput()
                })
            } catch (error) {
                console.error('Failed to load messages:', error)
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

            // IMMEDIATELY add user message to UI for instant feedback
            // DO NOT EVER REMOVE THIS!
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

            // Add user message to messages array immediately
            this.messages.push(userMessage)

            // Scroll to show the new user message
            this.$nextTick(() => {
                this.scrollToBottom(true)
            })

            // Clear input immediately for better UX
            this.messageInput = ''
            this.attachedImage = null
            this.sending = true

            try {
                let apiThreadId = this.currentThreadId

                // If this is a temporary thread, create it on the backend first
                if (this.currentThreadId.startsWith('temp_')) {
                    console.log('🔄 Converting temp thread to real thread...')

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

                    this.currentThreadId = apiThreadId
                    this.currentThread.id = apiThreadId

                    console.log('✅ Thread converted to real ID:', apiThreadId)
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
                console.error('Failed to send message:', error)
                this.showError('Failed to send message')
            } finally {
                this.sending = false
                this.streaming = false

                // DON'T reload messages since we already show user message immediately
                // and backend will only send assistant response via streaming or sync response

                // Keep focus on mobile to prevent keyboard closing - enhanced version
                this.$nextTick(async () => {
                    await this.maintainMobileFocus()
                })
            }
        },

        // Method to stop streaming
        stopStreaming() {
            console.log('🛑 Stopping streaming...')

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
                console.log('📨 Starting streaming for thread:', threadId)

                const headers = {
                    'Content-Type': 'application/json',
                    'Telegram-Init-Data': window.Telegram?.WebApp?.initData || '',
                }

                if (this.csrfToken) {
                    headers['X-CSRF-Token'] = this.csrfToken
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
                                        console.log('✅ Streaming completed')
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
                                            // First update - create assistant message with estimated height
                                            const assistantMessage = {
                                                id: data.id,
                                                role: 'assistant',
                                                content: data.content || '',
                                                created_at:
                                                    data.created_at ||
                                                    new Date().toISOString(),
                                                is_live: true,
                                                message_type: 'normal',
                                                is_complete: false,
                                                isStreaming: true,
                                                estimatedHeight: this.estimateMessageHeight(
                                                    data.content || ''
                                                ),
                                            }
                                            this.messages.push(assistantMessage)
                                            streamingMessageId = data.id

                                            // Pre-expand container and scroll immediately without smooth animation
                                            this.$nextTick(() => {
                                                this.scrollToBottom(false, true)
                                            })
                                        } else {
                                            // Buffer the content and throttle updates
                                            this.streamingBuffer = data.content || ''

                                            const now = Date.now()
                                            if (
                                                now - lastUpdateTime >=
                                                this.streamingThrottleMs
                                            ) {
                                                this.updateStreamingMessage(
                                                    streamingMessageId,
                                                    this.streamingBuffer
                                                )
                                                lastUpdateTime = now
                                            } else {
                                                // Clear any pending update and schedule new one
                                                if (this.streamingUpdateTimer) {
                                                    clearTimeout(this.streamingUpdateTimer)
                                                }
                                                this.streamingUpdateTimer = setTimeout(() => {
                                                    this.updateStreamingMessage(
                                                        streamingMessageId,
                                                        this.streamingBuffer
                                                    )
                                                    lastUpdateTime = Date.now()
                                                }, this.streamingThrottleMs - (now - lastUpdateTime))
                                            }
                                        }
                                    }
                                } catch (e) {
                                    console.error('Error parsing streaming data:', e)
                                }
                            }
                        }
                    }
                }
            } catch (error) {
                console.error('Streaming failed:', error)

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

        // Send message synchronously (non-streaming mode)
        async sendMessageSynchronously(threadId, messagePayload) {
            try {
                console.log(
                    '📨 Sending message synchronously for thread:',
                    threadId,
                    'payload:',
                    messagePayload
                )

                // Send message to backend and wait for complete response
                const response = await this.apiCall(`/api/threads/${threadId}/messages`, {
                    method: 'POST',
                    body: JSON.stringify(messagePayload),
                })

                console.log('✅ Synchronous response received:', response)

                // Add the assistant response to UI directly (like streaming does)
                if (response && response.content) {
                    const assistantMessage = {
                        id: response.id || Date.now(),
                        role: 'assistant',
                        content: response.content,
                        created_at: response.created_at || new Date().toISOString(),
                        is_live: true,
                        message_type: 'normal',
                        is_complete: true
                    }
                    this.messages.push(assistantMessage)
                    
                    // Scroll to bottom to show new response
                    this.$nextTick(() => {
                        this.scrollToBottom(true)
                    })
                }
            } catch (error) {
                console.error('Send message synchronously failed:', error)
                throw error
            }
        },

        // Remove all polling logic as we now use synchronous requests with SSE streaming

        handleKeyDown(event) {
            // Use the mobile keyboard composable for consistent detection
            const isMobile =
                this.mobileKeyboard?.isMobile?.value ||
                /Android|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(
                    navigator.userAgent
                ) ||
                'ontouchstart' in window ||
                window.matchMedia('(max-width: 768px)').matches

            if (event.key === 'Enter') {
                if (isMobile) {
                    // On mobile, Enter should create new line (don't prevent default)
                    // Only send on Shift+Enter or if textarea is empty (single line mode)
                    if (
                        event.shiftKey ||
                        (!event.shiftKey &&
                            this.messageInput.trim() &&
                            this.messageInput.indexOf('\n') === -1)
                    ) {
                        if (event.shiftKey || this.messageInput.indexOf('\n') === -1) {
                            event.preventDefault()
                            this.sendMessage()
                        }
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
                console.log('Thread settings updated locally:', this.threadSettings)
            }

            // Save user preferences when model or role changes
            this.saveUserPreference('selectedModel', this.threadSettings.model_name)
            this.saveUserPreference('selectedRole', this.threadSettings.role_id)

            // Focus the message input after changing settings
            this.$nextTick(() => {
                this.focusInput()
            })
        },

        async saveSettings() {
            console.log('saveSettings called, currentThreadId:', this.currentThreadId)
            console.log('threadSettings:', this.threadSettings)

            if (!this.currentThreadId) {
                console.log('No currentThreadId, returning')
                return
            }

            // Don't save for temp threads - they'll be saved with first message
            if (this.currentThreadId.startsWith('temp_')) {
                console.log('Temp thread, calling updateThreadSettings')
                this.updateThreadSettings()
                this.showSettings = false
                return
            }

            try {
                console.log('Making API call to update settings')
                await this.apiCall(`/api/threads/${this.currentThreadId}/settings`, {
                    method: 'PUT',
                    body: JSON.stringify(this.threadSettings),
                })

                console.log('Settings saved successfully')

                // Update current thread settings
                if (this.currentThread) {
                    this.currentThread.settings = { ...this.threadSettings }
                }

                // Save user preferences when settings are saved
                await this.saveUserPreferences()

                this.showSettings = false

                // Refocus input after closing settings
                this.$nextTick(() => {
                    this.focusInput()
                })
            } catch (error) {
                console.error('Failed to save settings:', error)
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
                console.log('Reloaded roles response:', response)
                this.roles = response.roles || []
                console.log('Updated roles array:', this.roles)

                this.showRoleEditor = false

                // Refocus input after role operations
                this.$nextTick(() => {
                    this.focusInput()
                })
            } catch (error) {
                console.error('Failed to save role:', error)
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
                console.error('Failed to delete role:', error)
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

            try {
                // Simple but effective markdown-like formatting without external libraries
                let formatted = content

                // Escape HTML first to prevent XSS
                formatted = formatted
                    .replace(/&/g, '&amp;')
                    .replace(/</g, '&lt;')
                    .replace(/>/g, '&gt;')
                    .replace(/"/g, '&quot;')
                    .replace(/'/g, '&#039;')

                // Apply basic markdown formatting
                formatted = formatted
                    // Code blocks (triple backticks)
                    .replace(/```([\s\S]*?)```/g, '<pre><code>$1</code></pre>')
                    // Inline code (single backticks)
                    .replace(/`([^`]+)`/g, '<code>$1</code>')
                    // Bold (**text** or __text__)
                    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
                    .replace(/__([^_]+)__/g, '<strong>$1</strong>')
                    // Italic (*text* or _text_)
                    .replace(/\*([^*]+)\*/g, '<em>$1</em>')
                    .replace(/_([^_]+)_/g, '<em>$1</em>')
                    // Strikethrough (~~text~~)
                    .replace(/~~([^~]+)~~/g, '<s>$1</s>')
                    // Links [text](url)
                    .replace(
                        /\[([^\]]+)\]\(([^)]+)\)/g,
                        '<a href="$2" target="_blank" rel="noopener">$1</a>'
                    )
                    // Line breaks
                    .replace(/\n/g, '<br>')

                // Process lists (simple implementation)
                const lines = formatted.split('<br>')
                let inList = false
                let listType = null

                for (let i = 0; i < lines.length; i++) {
                    const line = lines[i].trim()

                    // Unordered list
                    if (line.match(/^[-*+]\s+/)) {
                        if (!inList || listType !== 'ul') {
                            if (inList && listType !== 'ul') lines[i - 1] += `</${listType}>`
                            lines[i] = '<ul><li>' + line.replace(/^[-*+]\s+/, '') + '</li>'
                            inList = true
                            listType = 'ul'
                        } else {
                            lines[i] = '<li>' + line.replace(/^[-*+]\s+/, '') + '</li>'
                        }
                    }
                    // Ordered list
                    else if (line.match(/^\d+\.\s+/)) {
                        if (!inList || listType !== 'ol') {
                            if (inList && listType !== 'ol') lines[i - 1] += `</${listType}>`
                            lines[i] = '<ol><li>' + line.replace(/^\d+\.\s+/, '') + '</li>'
                            inList = true
                            listType = 'ol'
                        } else {
                            lines[i] = '<li>' + line.replace(/^\d+\.\s+/, '') + '</li>'
                        }
                    }
                    // End of list
                    else if (inList && line === '') {
                        lines[i - 1] += `</${listType}>`
                        inList = false
                        listType = null
                    }
                }

                // Close any remaining list
                if (inList && listType) {
                    lines[lines.length - 1] += `</${listType}>`
                }

                formatted = lines.join('<br>')

                return formatted
            } catch (error) {
                console.error('Error formatting message:', error)
                // Ultimate fallback - just escape HTML and add line breaks
                return content
                    .replace(/&/g, '&amp;')
                    .replace(/</g, '&lt;')
                    .replace(/>/g, '&gt;')
                    .replace(/\n/g, '<br>')
            }
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

                console.log(
                    isTemp
                        ? '✅ Temp thread deleted locally'
                        : '✅ Thread deleted from backend'
                )
            } catch (error) {
                console.error('Failed to delete thread:', error)
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
                console.error('Failed to archive thread:', error)
                this.showError('Failed to archive thread')
            }
        },

        toggleArchivedSection() {
            this.showArchivedSection = !this.showArchivedSection
        },

        // Enhanced scroll to bottom with auto-scroll for long chats
        scrollToBottom(smooth = true) {
            const container = this.$refs.messagesContainer
            if (container) {
                const scrollOptions = {
                    top: container.scrollHeight,
                    behavior: smooth ? 'smooth' : 'auto',
                }
                container.scrollTo(scrollOptions)
            }
        },

        // Auto-scroll when messages are loaded (for long chat history)
        autoScrollToBottom() {
            // Use a small delay to ensure DOM is fully rendered for large message lists
            this.$nextTick(() => {
                setTimeout(() => {
                    // For long message history, scroll immediately without smooth animation
                    this.scrollToBottom(false)
                }, 100)
            })
        },

        // Update streaming message content with smooth transitions
        updateStreamingMessage(messageId, content) {
            const message = this.messages.find(m => m.id === messageId)
            if (message) {
                // Use requestAnimationFrame for smooth DOM updates
                requestAnimationFrame(() => {
                    message.content = content

                    // Update estimated height for better layout stability
                    message.estimatedHeight = this.estimateMessageHeight(content)

                    // Maintain scroll position at bottom during streaming
                    this.$nextTick(() => {
                        const container = this.$refs.messagesContainer
                        if (container) {
                            const isNearBottom =
                                container.scrollHeight -
                                    container.scrollTop -
                                    container.clientHeight <
                                100
                            if (isNearBottom) {
                                this.scrollToBottom(true)
                            }
                        }
                    })
                })
            }
        },

        // Estimate message height to pre-allocate space
        estimateMessageHeight(content) {
            if (!content) return 60 // Minimum height

            // Rough estimation: ~20px per line, ~80 chars per line
            const lines = content.split('\n').length + Math.floor(content.length / 80)
            return Math.max(60, lines * 24 + 40) // Base padding + line height
        },

        // Enhanced typewriter effect for streaming (optional)
        enableTypewriterEffect(messageId, fullContent) {
            const message = this.messages.find(m => m.id === messageId)
            if (!message) return

            let currentIndex = 0
            const speed = 30 // milliseconds per character

            const typeNext = () => {
                if (currentIndex < fullContent.length && message.isStreaming) {
                    message.content = fullContent.substring(0, currentIndex + 1)
                    currentIndex++
                    setTimeout(typeNext, speed)
                } else {
                    message.content = fullContent
                }
            }

            typeNext()
        },

        // Mark any previous incomplete assistant messages as complete
        markPreviousMessagesComplete() {
            this.messages.forEach((message, index) => {
                if (message.role === 'assistant' && !message.is_complete) {
                    // Create a new message object to trigger reactivity
                    const updatedMessage = { ...message, is_complete: true }
                    this.messages.splice(index, 1, updatedMessage)
                }
            })
        },

        // DeviceStorage integration methods
        async loadUserPreferences() {
            try {
                console.log('📱 Loading user preferences from DeviceStorage...')

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

                console.log('✅ User preferences loaded:', this.userPreferences)
            } catch (error) {
                console.error('Failed to load user preferences:', error)
                // Use defaults on error
                this.userPreferences = {
                    selectedModel: 'gpt-4o',
                    selectedRole: null,
                    defaultTemperature: 1.0,
                    enableStreaming: true,
                }
            }
        },

        async loadCSRFToken() {
            try {
                console.log('🔐 Loading CSRF token...')
                const response = await fetch('/api/csrf-token', {
                    headers: {
                        'Telegram-Init-Data': window.Telegram?.WebApp?.initData || '',
                    },
                })

                if (response.ok) {
                    const data = await response.json()
                    this.csrfToken = data.csrf_token
                    console.log('✅ CSRF token loaded successfully')
                } else {
                    console.warn('Failed to load CSRF token:', response.status)
                }
            } catch (error) {
                console.error('Failed to load CSRF token:', error)
            }
        },

        async saveUserPreferences() {
            try {
                console.log('💾 Saving user preferences to DeviceStorage...')

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

                console.log('✅ User preferences saved:', this.userPreferences)
            } catch (error) {
                console.error('Failed to save user preferences:', error)
            }
        },

        async saveUserPreference(key, value) {
            try {
                this.userPreferences[key] = value
                await this.deviceStorage.setItem(key, value)
                console.log(`✅ User preference saved: ${key} =`, value)
            } catch (error) {
                console.error(`Failed to save user preference ${key}:`, error)
            }
        },

        // Mobile keyboard management methods
        setupMobileKeyboardHandling() {
            if (!this.mobileKeyboard.isMobile.value) return

            console.log('📱 Setting up mobile keyboard handling...')

            // Setup keyboard event handlers for mobile
            document.addEventListener('visibilitychange', () => {
                if (document.visibilityState === 'visible') {
                    // Re-focus when app becomes visible
                    setTimeout(() => this.focusInput(), 100)
                }
            })

            // Handle virtual keyboard show/hide on mobile
            if ('visualViewport' in window) {
                window.visualViewport.addEventListener('resize', () => {
                    // Adjust layout when virtual keyboard shows/hides
                    this.handleVirtualKeyboardResize()
                })
            }

            // Handle window resize for mobile browsers
            let resizeTimeout
            window.addEventListener('resize', () => {
                clearTimeout(resizeTimeout)
                resizeTimeout = setTimeout(() => {
                    this.handleMobileResize()
                }, 150)
            })
        },

        handleVirtualKeyboardResize() {
            if (!this.mobileKeyboard.isMobile.value) return

            const viewport = window.visualViewport
            const heightDiff = window.innerHeight - viewport.height

            // Adjust input container position when keyboard is shown
            const inputContainer = document.querySelector('.input-container')
            if (inputContainer) {
                if (heightDiff > 150) {
                    // Keyboard is likely shown
                    inputContainer.style.paddingBottom = '10px'
                } else {
                    inputContainer.style.paddingBottom = ''
                }
            }
        },

        handleMobileResize() {
            if (!this.mobileKeyboard.isMobile.value) return

            // Maintain focus after orientation change or keyboard events
            this.$nextTick(() => {
                setTimeout(() => this.focusInput(), 200)
            })
        },

        async focusInput() {
            const input = this.$refs.messageInput
            if (!input) return

            if (this.mobileKeyboard.isMobile.value) {
                await this.mobileKeyboard.maintainFocus(input)
            } else {
                input.focus()
            }
        },

        async maintainMobileFocus() {
            if (!this.mobileKeyboard.isMobile.value) return

            const input = this.$refs.messageInput
            if (input) {
                // Use enhanced mobile focus maintenance
                await this.mobileKeyboard.maintainFocus(input)
            }
        },

        // Input event handlers for enhanced mobile support
        onInputFocus(event) {
            console.log('🔍 Input focused')

            // Setup keyboard collision prevention on mobile
            if (this.mobileKeyboard.isMobile.value) {
                this.mobileKeyboard.preventKeyboardCollapse(event.target)

                // Adjust viewport on iOS to prevent zoom
                if (window.Telegram?.WebApp?.platform === 'ios') {
                    const viewport = document.querySelector('meta[name="viewport"]')
                    if (viewport) {
                        const originalContent = viewport.content
                        viewport.content = originalContent + ', user-scalable=no'

                        // Restore original viewport after a delay
                        setTimeout(() => {
                            viewport.content = originalContent
                        }, 1000)
                    }
                }
            }
        },

        onInputBlur(event) {
            console.log('🔍 Input blurred')

            // On mobile, prevent accidental blur by re-focusing
            if (
                this.mobileKeyboard.isMobile.value &&
                document.activeElement === document.body
            ) {
                setTimeout(() => {
                    if (this.$refs.messageInput && document.activeElement === document.body) {
                        console.log('🔍 Re-focusing input after accidental blur')
                        this.focusInput()
                    }
                }, 100)
            }
        },

        onInputTouch(event) {
            // Ensure the input is focused when touched on mobile
            if (this.mobileKeyboard.isMobile.value) {
                event.target.focus()
            }
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
            console.log('📷 Image selected:', file)
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
                console.log(
                    '✅ Image preview created:',
                    this.attachedImage.name,
                    'Preview length:',
                    this.attachedImage.preview.length
                )

                // Focus the message input after image selection
                this.$nextTick(() => {
                    this.focusInput()
                })
            }
            reader.readAsDataURL(file)
        },

        validateImageFile(file) {
            // Check file type
            const allowedTypes = [
                'image/jpeg',
                'image/jpg',
                'image/png',
                'image/gif',
                'image/webp',
            ]
            if (!allowedTypes.includes(file.type)) {
                this.showError(
                    `File type ${file.type} not supported. Please use JPEG, PNG, GIF, or WebP.`
                )
                return false
            }

            // Check file size (10MB limit)
            const maxSize = 10 * 1024 * 1024
            if (file.size > maxSize) {
                this.showError(`File ${file.name} is too large. Maximum size is 10MB.`)
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

// Global error handlers
window.addEventListener('error', function (e) {
    console.error('🚨 JavaScript Error:', e.error)
    console.error('📍 Error details:', {
        message: e.message,
        filename: e.filename,
        lineno: e.lineno,
        colno: e.colno,
        stack: e.error?.stack,
    })
})

window.addEventListener('unhandledrejection', function (e) {
    console.error('🚨 Unhandled Promise Rejection:', e.reason)
    console.error('📍 Promise rejection details:', e)
})
