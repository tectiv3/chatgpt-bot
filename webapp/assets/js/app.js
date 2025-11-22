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

const useMobileKeyboard = () => {
    const isMobile = computed(() => {
        return (
            /Android|iPhone|iPad|iPod|BlackBerry|IEMobile|Opera Mini/i.test(
                navigator.userAgent
            ) || 'ontouchstart' in window
        )
    })

    const focusInput = inputRef => {
        if (inputRef) {
            inputRef.focus()
        }
    }

    return {
        isMobile,
        focusInput,
    }
}

createApp({
    delimiters: ['[[', ']]'],
    data() {
        return {
            loading: true,
            messagesLoading: false,
            sidebarOpen: false,
            sidebarCollapsed: false, // Desktop sidebar collapse state

            // Shared data
            threads: [],
            archivedThreads: [],
            models: [],
            roles: [],

            // Split pane management
            panes: [], // Array of pane objects
            activePaneId: null, // Currently focused pane
            maxPanes: 4,
            dividerDragging: false,
            dragStartX: 0,
            dragPaneIndex: 0,

            // UI state
            creatingThread: false,
            showSettings: false,
            showRoleManager: false,
            showThreadSelector: false,
            threadSelectorPaneId: null,
            showRoleEditor: false,
            showArchivedSection: false,
            settingsPaneId: null, // Which pane's settings are being edited

            // Edit title modal state
            showEditTitle: false,
            editingTitle: {
                id: null,
                title: '',
            },
            savingTitle: false,

            // Persistent user preferences
            userPreferences: {
                selectedModel: 'gpt-4o',
                selectedRole: null,
                defaultTemperature: 1.0,
                enableStreaming: true,
                selectedThreadId: null,
                sidebarCollapsed: false,
                paneLayout: null, // Save pane layout
            },

            // Role editing
            editingRole: {
                id: null,
                name: '',
                prompt: '',
            },

            // Markdown processor instance (initialized on first use)
            markdownProcessor: null,

            // Fullscreen image viewer
            fullscreenImage: null,
        }
    },

    computed: {
        // Active pane getter
        activePane() {
            return this.panes.find(p => p.id === this.activePaneId) || this.panes[0]
        },

        // Backward compatibility - delegate to active pane
        currentThreadId() {
            return this.activePane?.threadId || null
        },

        currentThread() {
            return this.activePane?.thread || null
        },

        messages() {
            return this.activePane?.messages || []
        },

        threadSettings() {
            return this.activePane?.settings || this.getDefaultSettings()
        },

        messageInput: {
            get() {
                return this.activePane?.messageInput || ''
            },
            set(value) {
                if (this.activePane) {
                    this.activePane.messageInput = value
                }
            },
        },

        attachedFile: {
            get() {
                return this.activePane?.attachedFile || null
            },
            set(value) {
                if (this.activePane) {
                    this.activePane.attachedFile = value
                }
            },
        },

        sending() {
            return this.activePane?.sending || false
        },

        streaming() {
            return this.activePane?.streaming || false
        },

        currentMessageCount() {
            return this.activePane?.messagesLoading ? '...' : this.messages.length
        },

        // Computed property for getting current role name
        currentRoleName() {
            if (!this.threadSettings?.role_id) return ''
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

        // Computed property to determine if message can be sent for active pane
        canSendMessage() {
            const pane = this.activePane
            if (!pane) return false
            return (
                !pane.sending &&
                !pane.streaming &&
                !this.creatingThread &&
                pane.messageInput.trim().length > 0 &&
                pane.threadId !== null
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

        // Check if streaming can be stopped
        canStopStreaming() {
            return this.streaming && this.activePane?.streamController
        },

        // Check if there are archived threads to show section
        hasArchivedThreads() {
            return this.archivedThreads.length > 0
        },

        // Should show role selector (only for new threads)
        shouldShowRoleSelector() {
            return this.currentThreadId && this.messages.length === 0
        },

        // Can split pane
        canSplitPane() {
            return this.panes.length < this.maxPanes && !this.mobileKeyboard?.isMobile?.value
        },

        // Can close pane
        canClosePane() {
            return this.panes.length > 1
        },
    },

    watch: {
        // // Watch message count and update thread accordingly
        // currentMessageCount(newCount) {
        //     if (this.currentThread) {
        //         this.currentThread.message_count = newCount
        //     }
        // },

        // Watch sidebar state to handle mobile scroll prevention
        sidebarOpen(isOpen) {
            if (window.innerWidth < 1024) {
                document.body.classList.toggle('sidebar-open', isOpen)
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

        // Hide initial loader and show Vue app with smooth transition
        this.hideInitialLoader()

        // Open sidebar by default on desktop/wide screens
        if (window.innerWidth >= 1024) {
            this.sidebarOpen = true
        }

        // Handle window resize to adapt sidebar behavior
        window.addEventListener('resize', this.handleWindowResize)

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
        window.removeEventListener('resize', this.handleWindowResize)

        // Clean up any active streaming and timers
        if (this.streamingUpdateTimer) {
            clearTimeout(this.streamingUpdateTimer)
        }
        this.stopStreaming()
    },

    methods: {
        // ==================== PANE MANAGEMENT ====================

        createPane(threadId = null) {
            const paneId = 'pane_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9)
            const pane = {
                id: paneId,
                threadId: threadId,
                thread: null,
                messages: [],
                messageInput: '',
                attachedFile: null,
                sending: false,
                streaming: false,
                streamController: null,
                streamingBuffer: '',
                streamingUpdateTimer: null,
                messagesLoading: false,
                widthPercent: 100 / (this.panes.length + 1), // Will be recalculated
                settings: this.getDefaultSettings(),
            }
            return pane
        },

        getDefaultSettings() {
            return {
                model_name: this.userPreferences?.selectedModel || 'gpt-4o',
                temperature: this.userPreferences?.defaultTemperature || 1.0,
                role_id: this.userPreferences?.selectedRole || null,
                lang: 'en',
                master_prompt:
                    "You are a helpful assistant. You always try to answer truthfully. If you don't know the answer, just say that you don't know, don't try to make up an answer. Don't explain yourself. Do not introduce yourself, just answer the user concisely.",
                context_limit: 40000,
                enabled_tools: ['search'],
            }
        },

        initializePanes() {
            // Create initial pane
            const pane = this.createPane()
            pane.widthPercent = 100
            this.panes = [pane]
            this.activePaneId = pane.id
        },

        splitPane(sourcePaneId = null) {
            if (!this.canSplitPane) return

            const sourcePane = sourcePaneId
                ? this.panes.find(p => p.id === sourcePaneId)
                : this.activePane

            if (!sourcePane) return

            // Create new pane
            const newPane = this.createPane()

            // Find source pane index and insert after it
            const sourceIndex = this.panes.findIndex(p => p.id === sourcePane.id)
            this.panes.splice(sourceIndex + 1, 0, newPane)

            // Recalculate widths
            this.recalculatePaneWidths()

            // Activate the new pane
            this.activePaneId = newPane.id

            // Save layout
            this.savePaneLayout()

            // Open thread selector for the new pane
            this.$nextTick(() => {
                this.openThreadSelectorForPane(newPane.id)
            })
        },

        closePane(paneId) {
            if (!this.canClosePane) return

            const paneIndex = this.panes.findIndex(p => p.id === paneId)
            if (paneIndex === -1) return

            const pane = this.panes[paneIndex]

            // Stop any streaming in this pane
            if (pane.streamController) {
                pane.streamController.abort()
            }
            if (pane.streamingUpdateTimer) {
                clearTimeout(pane.streamingUpdateTimer)
            }

            // Remove pane
            this.panes.splice(paneIndex, 1)

            // If closed pane was active, activate adjacent pane
            if (this.activePaneId === paneId) {
                const newActiveIndex = Math.min(paneIndex, this.panes.length - 1)
                this.activePaneId = this.panes[newActiveIndex].id
            }

            // Recalculate widths
            this.recalculatePaneWidths()

            // Save layout
            this.savePaneLayout()
        },

        setActivePane(paneId) {
            if (this.panes.find(p => p.id === paneId)) {
                this.activePaneId = paneId
            }
        },

        recalculatePaneWidths() {
            const equalWidth = 100 / this.panes.length
            this.panes.forEach(pane => {
                pane.widthPercent = equalWidth
            })
        },

        // Divider dragging
        startDividerDrag(event, paneIndex) {
            if (this.panes.length < 2) return

            this.dividerDragging = true
            this.dragStartX = event.clientX || event.touches?.[0]?.clientX
            this.dragPaneIndex = paneIndex

            document.addEventListener('mousemove', this.onDividerDrag)
            document.addEventListener('mouseup', this.stopDividerDrag)
            document.addEventListener('touchmove', this.onDividerDrag)
            document.addEventListener('touchend', this.stopDividerDrag)

            // Prevent text selection during drag and set cursor
            document.body.style.userSelect = 'none'
            document.body.classList.add('pane-dragging')
        },

        onDividerDrag(event) {
            if (!this.dividerDragging) return

            const clientX = event.clientX || event.touches?.[0]?.clientX
            const container = this.$refs.panesContainer
            if (!container) return

            const containerRect = container.getBoundingClientRect()
            const containerWidth = containerRect.width
            const relativeX = clientX - containerRect.left
            const percentX = (relativeX / containerWidth) * 100

            // Calculate cumulative width up to drag pane
            let cumulativeWidth = 0
            for (let i = 0; i < this.dragPaneIndex; i++) {
                cumulativeWidth += this.panes[i].widthPercent
            }

            // New width for the pane being resized
            const newWidth = percentX - cumulativeWidth
            const minWidth = 20 // Minimum 20% width
            const maxWidth = 100 - (this.panes.length - 1) * minWidth

            if (newWidth >= minWidth && newWidth <= maxWidth) {
                const leftPane = this.panes[this.dragPaneIndex]
                const rightPane = this.panes[this.dragPaneIndex + 1]

                if (leftPane && rightPane) {
                    const totalWidth = leftPane.widthPercent + rightPane.widthPercent
                    const rightNewWidth = totalWidth - newWidth

                    if (rightNewWidth >= minWidth) {
                        leftPane.widthPercent = newWidth
                        rightPane.widthPercent = rightNewWidth
                    }
                }
            }
        },

        stopDividerDrag() {
            this.dividerDragging = false
            document.removeEventListener('mousemove', this.onDividerDrag)
            document.removeEventListener('mouseup', this.stopDividerDrag)
            document.removeEventListener('touchmove', this.onDividerDrag)
            document.removeEventListener('touchend', this.stopDividerDrag)
            document.body.style.userSelect = ''
            document.body.classList.remove('pane-dragging')

            // Save layout after drag
            this.savePaneLayout()
        },

        savePaneLayout() {
            const layout = this.panes.map(p => ({
                threadId: p.threadId,
                widthPercent: p.widthPercent,
            }))
            this.saveUserPreference('paneLayout', layout)
        },

        async restorePaneLayout() {
            const layout = this.userPreferences.paneLayout

            if (layout && Array.isArray(layout) && layout.length > 0) {
                // Restore panes from layout
                this.panes = []
                for (const paneData of layout) {
                    // Create pane WITHOUT threadId (we'll set it via selectThreadInPane)
                    const pane = this.createPane()
                    pane.widthPercent = paneData.widthPercent || 100 / layout.length
                    this.panes.push(pane)

                    // Load thread data if threadId exists and thread is valid
                    if (paneData.threadId) {
                        const threadExists =
                            this.threads.find(t => t.id === paneData.threadId) ||
                            this.archivedThreads.find(t => t.id === paneData.threadId)
                        if (threadExists) {
                            await this.selectThreadInPane(pane.id, paneData.threadId)
                        }
                    }
                }
                this.activePaneId = this.panes[0]?.id
            } else {
                // Initialize with single pane
                this.initializePanes()

                // Try to restore last selected thread
                const savedThreadId = this.userPreferences.selectedThreadId
                if (savedThreadId) {
                    const threadExists =
                        this.threads.find(t => t.id === savedThreadId) ||
                        this.archivedThreads.find(t => t.id === savedThreadId)
                    if (threadExists) {
                        await this.selectThreadInPane(this.panes[0].id, savedThreadId)
                    }
                }
            }
        },

        // Toggle sidebar (desktop)
        toggleSidebar() {
            this.sidebarCollapsed = !this.sidebarCollapsed
            this.saveUserPreference('sidebarCollapsed', this.sidebarCollapsed)
        },

        // Get pane by ID helper
        getPaneById(paneId) {
            return this.panes.find(p => p.id === paneId)
        },

        // Open thread selector modal for a pane
        openThreadSelectorForPane(paneId) {
            this.threadSelectorPaneId = paneId
            this.showThreadSelector = true
        },

        // Select a thread for a pane (from modal)
        selectThreadForPane(paneId, threadId) {
            this.selectThreadInPane(paneId, threadId)
            this.showThreadSelector = false
            this.threadSelectorPaneId = null
        },

        // Create new thread for a pane (from modal)
        async createNewThreadForPane(paneId) {
            const pane = this.getPaneById(paneId)
            if (!pane) return

            // Set this pane as active so newThread updates the right pane
            this.setActivePane(paneId)
            await this.newThread()

            this.showThreadSelector = false
            this.threadSelectorPaneId = null
        },

        // Check if message can be sent for a specific pane
        canSendInPane(pane) {
            if (!pane) return false
            const hasContent = pane.messageInput.trim().length > 0 || !!pane.attachedFile
            return (
                !pane.sending &&
                !pane.streaming &&
                !this.creatingThread &&
                hasContent &&
                pane.threadId !== null
            )
        },

        // Send message in a specific pane
        sendMessageInPane(paneId) {
            const pane = this.getPaneById(paneId)
            if (!pane || !this.canSendInPane(pane)) return

            // Set this pane as active
            this.setActivePane(paneId)

            // Call the main send message logic
            this.sendMessage()
        },

        // Stop streaming in a specific pane
        stopStreamingInPane(paneId) {
            const pane = this.getPaneById(paneId)
            if (!pane) return

            if (pane.streamController) {
                pane.streamController.abort()
                pane.streamController = null
            }
            pane.streaming = false
        },

        // Clear attached file in a specific pane
        clearAttachedFileInPane(paneId) {
            const pane = this.getPaneById(paneId)
            if (pane) {
                pane.attachedFile = null
            }
        },

        // Toggle tool in a specific pane
        toggleToolInPane(paneId, toolName) {
            const pane = this.getPaneById(paneId)
            if (!pane) return

            const tools = [...pane.settings.enabled_tools]
            const index = tools.indexOf(toolName)

            if (index > -1) {
                tools.splice(index, 1)
            } else {
                tools.push(toolName)
            }

            pane.settings.enabled_tools = tools

            if (pane.thread) {
                pane.thread.settings = { ...pane.settings }
            }

            if (pane.threadId) {
                this.saveSettingsForPane(paneId)
            }

            if (!this.mobileKeyboard.isMobile.value) {
                this.$nextTick(() => this.focusInput())
            }
        },

        // Handle keydown in a specific pane
        handleKeyDownInPane(event, paneId) {
            if (event.key === 'Enter') {
                if (this.mobileKeyboard.isMobile.value) {
                    // On mobile, only send on Shift+Enter
                    if (event.shiftKey) {
                        event.preventDefault()
                        this.sendMessageInPane(paneId)
                    }
                } else {
                    // On desktop, Enter sends message, Shift+Enter creates new line
                    if (!event.shiftKey) {
                        event.preventDefault()
                        this.sendMessageInPane(paneId)
                    }
                }
            }

            // Handle Escape key to stop streaming
            const pane = this.getPaneById(paneId)
            if (event.key === 'Escape' && pane?.streaming) {
                event.preventDefault()
                this.stopStreamingInPane(paneId)
            }
        },

        // Save settings for a specific pane
        saveSettingsForPane(paneId) {
            const pane = this.getPaneById(paneId)
            if (!pane || !pane.threadId) return

            // Use existing saveSettings logic but for specific pane
            fetch(`/api/threads/${pane.threadId}/settings`, {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json',
                    'X-Init-Data': this.initData,
                },
                body: JSON.stringify({
                    model_name: pane.settings.model_name,
                    role_id: pane.settings.role_id,
                    enabled_tools: pane.settings.enabled_tools,
                }),
            }).catch(err => console.error('Error saving pane settings:', err))
        },

        // ==================== END PANE MANAGEMENT ====================

        hideInitialLoader() {
            const loader = document.getElementById('initial-loader')
            const app = document.getElementById('app')

            if (loader && app) {
                // Start fade out animation
                loader.classList.add('fade-out')

                // Show Vue app
                app.style.visibility = 'visible'

                // Remove loader after animation completes
                setTimeout(() => {
                    if (loader.parentNode) {
                        loader.parentNode.removeChild(loader)
                    }
                }, 300) // Match CSS transition duration
            }
        },

        adaptToTelegramTheme() {
            const webApp = window.Telegram?.WebApp
            const root = document.documentElement

            // Set CSS variables for Telegram theme colors
            if (webApp?.themeParams) {
                // Apply all available theme parameters
                root.style.setProperty(
                    '--tg-theme-bg-color',
                    webApp.themeParams.bg_color ||
                        webApp.themeParams.background_color ||
                        '#ffffff'
                )
                root.style.setProperty(
                    '--tg-theme-text-color',
                    webApp.themeParams.text_color || '#000000'
                )
                root.style.setProperty(
                    '--tg-theme-hint-color',
                    webApp.themeParams.hint_color || '#999999'
                )
                root.style.setProperty(
                    '--tg-theme-link-color',
                    webApp.themeParams.link_color || '#2481cc'
                )
                root.style.setProperty(
                    '--tg-theme-button-color',
                    webApp.themeParams.button_color || '#2481cc'
                )
                root.style.setProperty(
                    '--tg-theme-button-text-color',
                    webApp.themeParams.button_text_color || '#ffffff'
                )
                root.style.setProperty(
                    '--tg-theme-secondary-bg-color',
                    webApp.themeParams.secondary_bg_color || '#f1f1f1'
                )
                root.style.setProperty(
                    '--tg-theme-header-bg-color',
                    webApp.themeParams.header_bg_color ||
                        webApp.themeParams.bg_color ||
                        '#ffffff'
                )
                root.style.setProperty(
                    '--tg-theme-accent-text-color',
                    webApp.themeParams.accent_text_color ||
                        webApp.themeParams.link_color ||
                        '#2481cc'
                )
                root.style.setProperty(
                    '--tg-theme-section-bg-color',
                    webApp.themeParams.section_bg_color ||
                        webApp.themeParams.secondary_bg_color ||
                        '#f1f1f1'
                )
                root.style.setProperty(
                    '--tg-theme-section-header-text-color',
                    webApp.themeParams.section_header_text_color ||
                        webApp.themeParams.link_color ||
                        '#2481cc'
                )
                root.style.setProperty(
                    '--tg-theme-subtitle-text-color',
                    webApp.themeParams.subtitle_text_color ||
                        webApp.themeParams.hint_color ||
                        '#999999'
                )
                root.style.setProperty(
                    '--tg-theme-destructive-text-color',
                    webApp.themeParams.destructive_text_color || '#ff3b30'
                )

                document.body.classList.toggle('tg-dark-theme', webApp.colorScheme === 'dark')
                document.body.style.backgroundColor = webApp.themeParams.bg_color || '#ffffff'
                document.body.style.color = webApp.themeParams.text_color || '#000000'
            } else {
                // Fallback colors for development
                console.log('Telegram WebApp theme not available, using default light theme')

                // Set light theme defaults
                root.style.setProperty('--tg-theme-bg-color', '#ffffff')
                root.style.setProperty('--tg-theme-text-color', '#000000')
                root.style.setProperty('--tg-theme-hint-color', '#999999')
                root.style.setProperty('--tg-theme-link-color', '#2481cc')
                root.style.setProperty('--tg-theme-button-color', '#2481cc')
                root.style.setProperty('--tg-theme-button-text-color', '#ffffff')
                root.style.setProperty('--tg-theme-secondary-bg-color', '#f1f1f1')
                root.style.setProperty('--tg-theme-header-bg-color', '#ffffff')
                root.style.setProperty('--tg-theme-accent-text-color', '#2481cc')
                root.style.setProperty('--tg-theme-section-bg-color', '#f1f1f1')
                root.style.setProperty('--tg-theme-section-header-text-color', '#2481cc')
                root.style.setProperty('--tg-theme-subtitle-text-color', '#999999')
                root.style.setProperty('--tg-theme-destructive-text-color', '#ff3b30')

                document.body.style.backgroundColor = '#ffffff'
                document.body.style.color = '#000000'
            }

            // Set viewport height correctly for the app container
            if (webApp?.viewportHeight) {
                document.documentElement.style.setProperty(
                    '--tg-viewport-height',
                    `${webApp.viewportHeight}px`
                )
                document.body.style.height = `${webApp.viewportHeight}px`
            } else {
                // Fallback for development or non-Telegram environments
                document.documentElement.style.setProperty('--tg-viewport-height', '100vh')
                document.body.style.height = '100vh'
            }

            // Handle viewport stable height for mobile keyboards
            if (
                webApp?.viewportStableHeight &&
                webApp.viewportStableHeight !== webApp.viewportHeight
            ) {
                document.documentElement.style.setProperty(
                    '--tg-stable-height',
                    `${webApp.viewportStableHeight}px`
                )
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

                // Restore sidebar collapsed state
                this.sidebarCollapsed = this.userPreferences.sidebarCollapsed || false

                // Initialize and restore pane layout
                await this.restorePaneLayout()
            } catch (error) {
                this.showError('Failed to load data.')
            }
        },

        // Pane-aware thread selection
        async selectThreadInPane(paneId, threadId) {
            const pane = this.getPaneById(paneId)
            if (!pane) return

            if (pane.threadId === threadId) return

            pane.messagesLoading = true
            pane.threadId = threadId
            pane.thread =
                this.threads.find(t => t.id === threadId) ||
                this.archivedThreads.find(t => t.id === threadId)

            // Close sidebar on mobile
            this.sidebarOpen = false

            if (pane.thread) {
                // Ensure thread settings exist, fallback to defaults
                pane.settings = {
                    ...this.getDefaultSettings(),
                    ...pane.thread.settings,
                }

                try {
                    await this.loadPaneMessages(paneId)
                } catch (error) {
                    console.error('Failed to load messages:', error)
                    this.showError('Failed to load thread messages')
                }
            } else {
                console.error('Thread not found:', threadId)
                this.showError('Thread not found')
            }

            // Save layout
            this.savePaneLayout()
        },

        async loadPaneThread(paneId, threadId) {
            await this.selectThreadInPane(paneId, threadId)
        },

        async loadPaneMessages(paneId) {
            const pane = this.getPaneById(paneId)
            if (!pane || !pane.threadId) return

            pane.messagesLoading = true

            try {
                const response = await this.apiCall(`/api/threads/${pane.threadId}/messages`)
                const newMessages = response.messages || []

                // Process messages
                const processedMessages = newMessages.map(message => ({
                    ...message,
                    is_complete: message.is_complete !== false,
                    formattedContent: this.formatMessage(message.content, message.annotations),
                }))

                pane.messages = processedMessages

                // Auto-scroll this pane to bottom
                this.autoScrollToBottom(pane.id)
            } catch (error) {
                this.showError('Failed to load messages')
            } finally {
                pane.messagesLoading = false
            }
        },

        // Backward compatible wrapper
        async restoreSelectedThread() {
            // This is now handled by restorePaneLayout
            // Kept for compatibility
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

        async newThread() {
            // Prevent multiple simultaneous thread creations
            if (this.creatingThread) return

            this.creatingThread = true

            try {
                // Close sidebar
                this.sidebarOpen = false

                const pane = this.activePane
                if (!pane) {
                    this.initializePanes()
                }

                // Reset thread settings using user preferences as defaults
                const newSettings = this.getDefaultSettings()

                const response = await this.apiCall('/api/threads', {
                    method: 'POST',
                    body: JSON.stringify({
                        initial_message: '', // Backend requires this field
                        settings: newSettings,
                    }),
                })

                // Extract thread ID from response (might be nested in data)
                const threadId = response.thread_id || response.data?.thread_id

                if (!threadId) {
                    throw new Error('Thread creation failed - no thread ID returned')
                }

                // Add real thread to list
                const newThread = {
                    id: threadId,
                    title: 'New Thread',
                    message_count: 0,
                    created_at: new Date().toISOString(),
                    updated_at: new Date().toISOString(),
                    settings: { ...newSettings },
                }

                this.threads.unshift(newThread)

                // Update the active pane with the new thread
                const currentPane = this.activePane
                if (currentPane) {
                    currentPane.threadId = threadId
                    currentPane.thread = newThread
                    currentPane.messages = []
                    currentPane.settings = { ...newSettings }
                    currentPane.messageInput = ''
                    currentPane.attachedFile = null
                }

                await this.saveUserPreference('selectedThreadId', threadId)
                this.savePaneLayout()

                this.$nextTick(() => this.focusInput())
            } catch (error) {
                this.showError(`Failed to create new thread: ${error.message}`)
            } finally {
                this.creatingThread = false
            }
        },

        async selectThread(threadId) {
            // Select thread in the active pane
            if (!this.activePane) {
                this.initializePanes()
            }
            await this.selectThreadInPane(this.activePaneId, threadId)
            await this.saveUserPreference('selectedThreadId', threadId)
            this.$nextTick(() => this.focusInput())
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
            if (!this.activePane || !this.activePane.threadId) return
            await this.loadPaneMessages(this.activePaneId)
            this.$nextTick(() => this.focusInput())
        },

        async sendMessage() {
            const pane = this.activePane
            if (!pane) return

            const paneId = pane.id

            const attachedFileSnapshot = pane.attachedFile
                ? {
                      preview: pane.attachedFile.preview,
                      name: pane.attachedFile.name,
                      file: pane.attachedFile.file,
                      isImage: !!pane.attachedFile.isImage,
                      mimeType: pane.attachedFile.mimeType,
                  }
                : null
            const hasFile = attachedFileSnapshot !== null

            if (
                (!pane.messageInput.trim() && !hasFile) ||
                pane.sending ||
                !pane.threadId ||
                !pane.thread
            ) {
                if (!pane.threadId || !pane.thread) {
                    this.showError('No thread selected')
                }
                return
            }

            const message = pane.messageInput.trim()

            let messageType = 'normal'
            if (hasFile) {
                messageType = attachedFileSnapshot.isImage ? 'image' : 'file'
            }

            const userMessage = {
                id: 'temp_' + Date.now(),
                role: 'user',
                content: message,
                created_at: new Date().toISOString(),
                is_live: true,
                message_type: messageType,
                image_data:
                    hasFile && attachedFileSnapshot.isImage
                        ? attachedFileSnapshot.preview
                        : null,
                image_name: hasFile ? attachedFileSnapshot.name : null,
                file_type: hasFile ? attachedFileSnapshot.mimeType : null,
                is_complete: true,
            }

            const targetPane = this.getPaneById(paneId)
            if (!targetPane) return

            targetPane.messages.push(userMessage)
            this.$nextTick(() => this.scrollToBottom(true))

            targetPane.messageInput = ''
            targetPane.attachedFile = null
            targetPane.sending = true

            try {
                const titleSource = message || (hasFile ? attachedFileSnapshot.name : '')
                if (
                    targetPane.thread &&
                    (targetPane.thread.title === 'New Conversation' ||
                        targetPane.thread.title === 'New Thread') &&
                    titleSource
                ) {
                    const newTitle =
                        titleSource.substring(0, 50) + (titleSource.length > 50 ? '...' : '')
                    targetPane.thread.title = newTitle
                    const threadIndex = this.threads.findIndex(
                        t => t.id === targetPane.threadId
                    )
                    if (threadIndex !== -1) {
                        this.threads[threadIndex].title = newTitle
                    }
                }

                const messagePayload = { message }
                if (hasFile && attachedFileSnapshot) {
                    messagePayload.image = {
                        data: attachedFileSnapshot.preview.split(',')[1],
                        filename: attachedFileSnapshot.name,
                        mime_type: attachedFileSnapshot.mimeType,
                    }
                }

                await this.sendMessageWithStreamingInPane(paneId, messagePayload)
            } catch (error) {
                this.showError(`Failed to send message: ${error.message}`)
            } finally {
                targetPane.sending = false
                targetPane.streaming = false
                this.$nextTick(() => this.focusInput())
            }
        },

        // Method to stop streaming
        stopStreaming() {
            const pane = this.activePane
            if (pane?.streamController) {
                pane.streamController.abort()
                pane.streamController = null
            }
            if (pane) {
                pane.streaming = false
            }
        },

        // Send message with Server-Sent Events streaming (pane-aware)
        async sendMessageWithStreamingInPane(paneId, messagePayload) {
            const pane = this.getPaneById(paneId)
            if (!pane) return

            pane.streaming = true
            pane.streamController = new AbortController()
            pane.streamingBuffer = ''

            let streamingMessageId = null
            let lastUpdateTime = 0
            const streamingThrottleMs = 50

            try {
                const headers = {
                    'Content-Type': 'application/json',
                    'Telegram-Init-Data': window.Telegram?.WebApp?.initData || '',
                }

                const response = await fetch(`/api/threads/${pane.threadId}/messages`, {
                    method: 'POST',
                    headers: headers,
                    body: JSON.stringify(messagePayload),
                    signal: pane.streamController.signal,
                })

                if (!response.ok) {
                    const errorText = await response.text()
                    throw new Error(
                        `HTTP ${response.status}: ${response.statusText} - ${errorText}`
                    )
                }

                const reader = response.body.getReader()
                const decoder = new TextDecoder()

                while (true) {
                    const { done, value } = await reader.read()
                    if (done) break

                    const chunk = decoder.decode(value, { stream: true })
                    const lines = chunk.split('\n')

                    for (const line of lines) {
                        if (!line.startsWith('data: ')) continue
                        const jsonData = line.substring(6).trim()
                        if (!jsonData) continue

                        try {
                            const data = JSON.parse(jsonData)

                            if (data.type === 'complete') {
                                pane.streaming = false

                                if (streamingMessageId) {
                                    const message = pane.messages.find(
                                        m => m.id === streamingMessageId
                                    )
                                    if (message) {
                                        message.is_complete = true
                                        message.isStreaming = false

                                        message.formattedContent = this.formatMessage(
                                            message.content,
                                            message.annotations
                                        )

                                        if (message.input_tokens || message.output_tokens) {
                                            if (pane.thread) {
                                                pane.thread.total_input_tokens =
                                                    (pane.thread.total_input_tokens || 0) +
                                                    (message.input_tokens || 0)
                                                pane.thread.total_output_tokens =
                                                    (pane.thread.total_output_tokens || 0) +
                                                    (message.output_tokens || 0)

                                                // Also update in threads array
                                                const threadIndex = this.threads.findIndex(
                                                    t => t.id === pane.threadId
                                                )
                                                if (threadIndex !== -1) {
                                                    this.threads[
                                                        threadIndex
                                                    ].total_input_tokens =
                                                        pane.thread.total_input_tokens
                                                    this.threads[
                                                        threadIndex
                                                    ].total_output_tokens =
                                                        pane.thread.total_output_tokens
                                                }
                                            }
                                        }
                                    }

                                    this.generateTitleFromConversationInPane(
                                        paneId,
                                        streamingMessageId
                                    )
                                }

                                return
                            }

                            // Handle streaming message updates
                            if (data.role === 'assistant' && data.content !== undefined) {
                                if (!streamingMessageId) {
                                    const assistantMessage = {
                                        ...data,
                                        role: 'assistant',
                                        content: data.content || '',
                                        created_at:
                                            data.created_at || new Date().toISOString(),
                                        is_live: true,
                                        message_type: 'normal',
                                        is_complete: false,
                                        isStreaming: true,
                                    }
                                    pane.messages.push(assistantMessage)
                                    streamingMessageId = data.id

                                    this.$nextTick(() => this.scrollToBottom(false))
                                } else {
                                    pane.streamingBuffer = data.content || ''

                                    // Update meta information if available
                                    const message = pane.messages.find(
                                        m => m.id === streamingMessageId
                                    )

                                    // Update message metadata if provided in this update
                                    if (message && data.input_tokens !== undefined) {
                                        message.input_tokens = data.input_tokens
                                    }
                                    if (message && data.output_tokens !== undefined) {
                                        message.output_tokens = data.output_tokens
                                    }
                                    if (message && data.total_tokens !== undefined) {
                                        message.total_tokens = data.total_tokens
                                    }
                                    if (message && data.model_used !== undefined) {
                                        message.model_used = data.model_used
                                    }
                                    if (message && data.response_time_ms !== undefined) {
                                        message.response_time_ms = data.response_time_ms
                                    }
                                    if (message && data.finish_reason !== undefined) {
                                        message.finish_reason = data.finish_reason
                                    }

                                    if (message && data.annotations !== undefined) {
                                        message.annotations = data.annotations
                                    }
                                    message.formattedContent = this.formatMessage(
                                        message.content,
                                        message.annotations
                                    )
                                    const now = Date.now()

                                    if (now - lastUpdateTime >= streamingThrottleMs) {
                                        this.updateStreamingMessageInPane(
                                            paneId,
                                            streamingMessageId,
                                            pane.streamingBuffer
                                        )
                                        lastUpdateTime = now
                                    } else {
                                        if (pane.streamingUpdateTimer) {
                                            clearTimeout(pane.streamingUpdateTimer)
                                        }
                                        pane.streamingUpdateTimer = setTimeout(() => {
                                            this.updateStreamingMessageInPane(
                                                paneId,
                                                streamingMessageId,
                                                pane.streamingBuffer
                                            )
                                            lastUpdateTime = Date.now()
                                        }, streamingThrottleMs - (now - lastUpdateTime))
                                    }
                                }
                            }
                        } catch (e) {
                            // Error parsing streaming data
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
                        const message = pane.messages.find(m => m.id === streamingMessageId)
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
                pane.streaming = false
                pane.streamController = null

                // Clean up streaming state and ensure final update
                if (pane.streamingUpdateTimer) {
                    clearTimeout(pane.streamingUpdateTimer)
                    pane.streamingUpdateTimer = null
                }

                if (streamingMessageId && pane.streamingBuffer) {
                    // Final update with any remaining buffer content
                    this.updateStreamingMessageInPane(
                        paneId,
                        streamingMessageId,
                        pane.streamingBuffer
                    )
                }

                if (streamingMessageId) {
                    const message = pane.messages.find(m => m.id === streamingMessageId)
                    if (message) {
                        message.isStreaming = false
                    }
                }

                pane.streamingBuffer = ''
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

        // Update current thread settings when dropdowns change (for draft threads)
        updateThreadSettings() {
            if (this.currentThread) {
                this.currentThread.settings = { ...this.threadSettings }
            }

            // Save user preferences when model or role changes
            this.saveUserPreference('selectedModel', this.threadSettings.model_name)
            this.saveUserPreference('selectedRole', this.threadSettings.role_id)

            if (!this.mobileKeyboard.isMobile.value) {
                this.$nextTick(() => this.focusInput())
            }
        },

        toggleTool(toolName) {
            const tools = [...this.threadSettings.enabled_tools]
            const index = tools.indexOf(toolName)

            if (index > -1) {
                tools.splice(index, 1)
            } else {
                tools.push(toolName)
            }

            this.threadSettings.enabled_tools = tools

            if (this.currentThread) {
                this.currentThread.settings = { ...this.threadSettings }
            }

            if (this.currentThreadId) {
                this.saveSettings()
            }

            if (!this.mobileKeyboard.isMobile.value) {
                this.$nextTick(() => this.focusInput())
            }
        },

        async saveSettings() {
            if (!this.currentThreadId) {
                await this.saveUserPreferences()
                this.showSettings = false
                if (!this.mobileKeyboard.isMobile.value) {
                    this.$nextTick(() => this.focusInput())
                }
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

                if (!this.mobileKeyboard.isMobile.value) {
                    this.$nextTick(() => this.focusInput())
                }
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
                    await this.apiCall(`/api/roles/${this.editingRole.id}`, {
                        method: 'PUT',
                        body: JSON.stringify({
                            name: this.editingRole.name,
                            prompt: this.editingRole.prompt,
                        }),
                    })
                } else {
                    await this.apiCall('/api/roles', {
                        method: 'POST',
                        body: JSON.stringify({
                            name: this.editingRole.name,
                            prompt: this.editingRole.prompt,
                        }),
                    })
                }

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

                this.roles = this.roles.filter(r => r.id !== roleId)

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
            const date = new Date(dateStr)
            const now = new Date()
            const diffMs = now - date
            const diffHours = Math.floor(diffMs / (1000 * 60 * 60))

            if (diffHours >= 24) {
                // Format: "Dec 15, 14:30"
                return (
                    date.toLocaleDateString('en-US', {
                        month: 'short',
                        day: 'numeric',
                    }) +
                    ', ' +
                    date.toLocaleTimeString('en-GB', {
                        hour: '2-digit',
                        minute: '2-digit',
                        hour12: false,
                    })
                )
            }

            return date.toLocaleTimeString('en-GB', {
                hour: '2-digit',
                minute: '2-digit',
                hour12: false,
            })
        },

        formatMessage(content, annotations) {
            if (!content) return ''

            let processedContent = content
            const urlCitations = []

            if (annotations && annotations.length > 0) {
                const urlAnnotations = annotations.filter(a => a.type === 'url_citation')

                const sortedByPosition = [...urlAnnotations].sort(
                    (a, b) => a.start_index - b.start_index
                )

                const citationMap = new Map()
                sortedByPosition.forEach((annotation, index) => {
                    citationMap.set(annotation, index + 1)
                })

                const sortedForReplacement = [...urlAnnotations].sort(
                    (a, b) => b.start_index - a.start_index
                )

                sortedForReplacement.forEach(annotation => {
                    const citationNumber = citationMap.get(annotation)

                    if (
                        annotation.start_index !== undefined &&
                        annotation.end_index !== undefined
                    ) {
                        const before = processedContent.substring(0, annotation.start_index)
                        const after = processedContent.substring(annotation.end_index)
                        let url = annotation.url || '#'
                        url = url
                            .replace(/[?&]utm_source=openai(&|$)/, '$1')
                            .replace(/\?$/, '')
                        processedContent = before + `[[${citationNumber}]](${url})` + after
                    }
                })

                urlCitations.push(...sortedByPosition)
            }

            if (!this.markdownProcessor) {
                this.markdownProcessor = window.markdownit({
                    html: false,
                    linkify: true,
                    typographer: true,
                    breaks: true,
                })
            }

            return this.markdownProcessor.render(processedContent)
        },

        formatAnnotations(annotations) {
            if (!annotations || annotations.length === 0) return ''

            const urlCitations = annotations.filter(a => a.type === 'url_citation')
            if (urlCitations.length === 0) return ''

            let html = '<div class="mt-3 pt-3 border-t border-white/10 text-sm">'
            html += '<div class="text-xs text-tg-hint mb-2">References:</div>'

            urlCitations.forEach((citation, index) => {
                const num = index + 1
                const title = citation.title || citation.url || 'Link'
                let url = citation.url || '#'
                url = url.replace(/[?&]utm_source=openai(&|$)/, '$1').replace(/\?$/, '')

                html += `<div class="mb-1">
                    <span class="text-tg-hint">[${num}]</span>
                    <a href="${url}" target="_blank" rel="noopener noreferrer"
                       class="text-tg-link hover:underline"
                       title="${url}">
                        ${this.escapeHtml(title)}
                        <i class="fas fa-external-link-alt text-xs ml-1"></i>
                    </a>
                </div>`
            })

            html += '</div>'
            return html
        },

        escapeHtml(text) {
            const div = document.createElement('div')
            div.textContent = text
            return div.innerHTML
        },

        // Helper method to get display content with streaming indicator
        getMessageDisplayContent(message) {
            if (message.isStreaming && message.displayContent) {
                return message.displayContent
            }
            return message.content
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
        async confirmDeleteThread(threadId) {
            // Use browser confirm dialog
            const confirmed = confirm(
                'Are you sure you want to delete this thread? This cannot be undone.'
            )
            if (!confirmed) return

            try {
                // Make API call to delete thread
                await this.apiCall(`/api/threads/${threadId}`, {
                    method: 'DELETE',
                })

                // Remove from local arrays
                this.threads = this.threads.filter(t => t.id !== threadId)
                this.archivedThreads = this.archivedThreads.filter(t => t.id !== threadId)

                // If currently selected thread was deleted, clear selection
                if (this.currentThreadId === threadId) {
                    this.currentThreadId = null
                    this.currentThread = null
                    this.messages = []

                    // Clear from storage
                    await this.saveUserPreference('selectedThreadId', null)

                    // Auto-select next available thread or clear if none
                    if (this.threads.length > 0) {
                        await this.selectThread(this.threads[0].id)
                    } else {
                        // Clear current selection if no threads left
                        this.currentThreadId = null
                        this.currentThread = null
                        this.messages = []
                    }
                }

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

                            // Clear from storage since thread is being archived
                            await this.saveUserPreference('selectedThreadId', null)

                            if (this.threads.length > 0) {
                                await this.selectThread(this.threads[0].id)
                            } else {
                                // Clear current selection if no threads left
                                this.currentThreadId = null
                                this.currentThread = null
                                this.messages = []
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

        scrollToBottom(smooth = true, paneId = null) {
            const targetPaneId = paneId || this.activePaneId
            const refName = 'messagesContainer_' + targetPaneId
            const container = this.$refs[refName]
            // Handle both array (from v-for) and single element refs
            const el = Array.isArray(container) ? container[0] : container
            if (el) {
                el.scrollTo({
                    top: el.scrollHeight,
                    behavior: smooth ? 'smooth' : 'auto',
                })
            }
        },

        autoScrollToBottom(paneId = null) {
            this.$nextTick(() => {
                setTimeout(() => this.scrollToBottom(false, paneId), 100)
            })
        },

        updateStreamingMessage(messageId, content) {
            const message = this.messages.find(m => m.id === messageId)
            if (message) {
                message.content = content
                // Add streaming cursor to the end of content during streaming
                if (message.isStreaming && content && content.length > 0) {
                    message.displayContent =
                        content + '<span class="streaming-indicator">▌</span>'
                } else {
                    message.displayContent = content
                }

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
            }
        },

        async generateTitleFromConversation(assistantMessageId) {
            // Only update if this is the first message exchange and title is still "New Thread"
            if (!this.currentThread || this.currentThread.title !== 'New Thread') return

            // Check if we have exactly 2 messages (user question + assistant response)
            if (this.messages.length !== 2) return

            const userMessage = this.messages.find(m => m.role === 'user')
            const assistantMessage = this.messages.find(m => m.id === assistantMessageId)

            if (!userMessage || !assistantMessage) return

            try {
                // Generate title from conversation
                const response = await this.apiCall(
                    `/api/threads/${this.currentThreadId}/generate-title`,
                    {
                        method: 'POST',
                        body: JSON.stringify({
                            question: userMessage.content,
                            response: assistantMessage.content,
                        }),
                    }
                )

                // Update the thread title in UI
                const newTitle = response.data?.title || response.title
                if (newTitle) {
                    this.currentThread.title = newTitle

                    // Update in threads array
                    const threadIndex = this.threads.findIndex(
                        t => t.id === this.currentThreadId
                    )
                    if (threadIndex !== -1) {
                        this.threads[threadIndex].title = newTitle
                    }
                }
            } catch (error) {
                // Silent failure for title generation - it's not critical
                // console.log('Failed to generate thread title:', error)
            }
        },

        // Pane-aware streaming message update
        updateStreamingMessageInPane(paneId, messageId, content) {
            const pane = this.getPaneById(paneId)
            if (!pane) return

            const message = pane.messages.find(m => m.id === messageId)
            if (message) {
                message.content = content
                if (message.isStreaming && content && content.length > 0) {
                    message.displayContent =
                        content + '<span class="streaming-indicator">▌</span>'
                } else {
                    message.displayContent = content
                }

                // Auto-scroll if this is the active pane
                if (pane.id === this.activePaneId) {
                    this.$nextTick(() => {
                        const container =
                            this.$refs[`messagesContainer_${paneId}`]?.[0] ||
                            this.$refs.messagesContainer
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
                }
            }
        },

        // Pane-aware title generation - only on first message exchange
        async generateTitleFromConversationInPane(paneId, assistantMessageId) {
            const pane = this.getPaneById(paneId)
            if (!pane?.thread) return

            // Only generate title on first exchange (2 messages: user + assistant)
            const userMessages = pane.messages.filter(m => m.role === 'user')
            const assistantMessages = pane.messages.filter(m => m.role === 'assistant')
            if (userMessages.length !== 1 || assistantMessages.length !== 1) return

            const userMessage = userMessages[0]
            const assistantMessage = pane.messages.find(m => m.id === assistantMessageId)
            if (!userMessage || !assistantMessage) return

            const question = userMessage.content || userMessage.image_name || ''
            if (!question) return
            try {
                const response = await this.apiCall(
                    `/api/threads/${pane.threadId}/generate-title`,
                    {
                        method: 'POST',
                        body: JSON.stringify({
                            question,
                            response: assistantMessage.content,
                        }),
                    }
                )

                const newTitle = response.data?.title || response.title
                if (newTitle) {
                    pane.thread.title = newTitle

                    const threadIndex = this.threads.findIndex(t => t.id === pane.threadId)
                    if (threadIndex !== -1) {
                        this.threads[threadIndex].title = newTitle
                    }
                }
            } catch (error) {
                // Silent failure for title generation
            }
        },

        // DeviceStorage integration methods
        async loadUserPreferences() {
            const [
                selectedModel,
                selectedRole,
                defaultTemperature,
                enableStreaming,
                selectedThreadId,
                sidebarCollapsed,
                paneLayout,
            ] = await Promise.all([
                this.deviceStorage.getItem('selectedModel', 'gpt-4o'),
                this.deviceStorage.getItem('selectedRole', null),
                this.deviceStorage.getItem('defaultTemperature', 1.0),
                this.deviceStorage.getItem('enableStreaming', true),
                this.deviceStorage.getItem('selectedThreadId', null),
                this.deviceStorage.getItem('sidebarCollapsed', false),
                this.deviceStorage.getItem('paneLayout', null),
            ])

            this.userPreferences = {
                selectedModel,
                selectedRole,
                defaultTemperature,
                enableStreaming,
                selectedThreadId,
                sidebarCollapsed,
                paneLayout,
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
                    selectedThreadId: this.currentThreadId, // Save current thread selection
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
                    this.deviceStorage.setItem(
                        'selectedThreadId',
                        this.userPreferences.selectedThreadId
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

        dismissKeyboard() {
            // Only dismiss keyboard on mobile when clicking outside textarea
            if (this.mobileKeyboard.isMobile.value) {
                const input = this.$refs.messageInput
                if (input && document.activeElement === input) {
                    input.blur()
                }
            }
        },

        onInputTouch(event) {
            event.target.focus()
        },

        selectFile() {
            const input = document.createElement('input')
            input.type = 'file'
            // Combine MIME types and extensions for best browser support
            input.setAttribute(
                'accept',
                'image/jpeg,image/png,image/gif,image/webp,' +
                    'application/pdf,text/plain,text/csv,' +
                    '.jpg,.jpeg,.png,.gif,.webp,' +
                    '.pdf,.txt,.csv,' +
                    '.py,.php,.go,.js,.vue,.ts,.tsx,' +
                    '.md,.json,.yaml,.yml,.xml,.html,.css,.sql,.sh'
            )
            input.style.display = 'none'
            document.body.appendChild(input)

            input.addEventListener('change', event => {
                this.handleFileSelect(event)
                document.body.removeChild(input)
            })

            input.addEventListener('cancel', () => {
                document.body.removeChild(input)
            })

            input.click()
        },

        handleFileSelect(event) {
            const file = event.target.files[0]
            if (!file) return

            if (!this.validateFile(file)) {
                // Validation failed - file not attached, user can try again
                return
            }

            const reader = new FileReader()
            const isImage = file.type.startsWith('image/')
            // Determine effective mime type (browsers may report code files as text/plain)
            const effectiveMimeType = this.getEffectiveMimeType(file)

            reader.onload = e => {
                this.attachedFile = {
                    file: file,
                    name: file.name,
                    size: file.size,
                    mimeType: effectiveMimeType,
                    isImage: isImage,
                    preview: e.target.result,
                }

                this.$nextTick(() => this.focusInput())
            }
            reader.readAsDataURL(file)
        },

        // Get effective mime type based on file extension (browsers often report code as text/plain)
        getEffectiveMimeType(file) {
            const ext = file.name.split('.').pop()?.toLowerCase()
            const extToMime = {
                py: 'text/x-python',
                php: 'text/x-php',
                go: 'text/x-go',
                js: 'text/javascript',
                vue: 'text/x-vue',
                ts: 'text/typescript',
                tsx: 'text/typescript',
                csv: 'text/csv',
                md: 'text/markdown',
                json: 'application/json',
                yaml: 'text/yaml',
                yml: 'text/yaml',
                xml: 'text/xml',
                html: 'text/html',
                css: 'text/css',
                sql: 'text/x-sql',
                sh: 'text/x-shellscript',
                bash: 'text/x-shellscript',
            }
            return extToMime[ext] || file.type
        },

        validateFile(file) {
            const allowedMimeTypes = [
                'image/jpeg',
                'image/jpg',
                'image/png',
                'image/gif',
                'image/webp',
                'application/pdf',
                'text/plain',
                'text/csv',
                'text/x-python',
                'text/x-php',
                'text/x-go',
                'text/javascript',
                'application/javascript',
                'text/x-vue',
                'text/typescript',
                'text/markdown',
                'application/json',
                'text/yaml',
                'text/xml',
                'text/html',
                'text/css',
                'text/x-sql',
                'text/x-shellscript',
            ]
            // Also check by extension for code files (browsers may report as text/plain or octet-stream)
            const allowedExtensions = [
                'jpg',
                'jpeg',
                'png',
                'gif',
                'webp',
                'pdf',
                'txt',
                'csv',
                'py',
                'php',
                'go',
                'js',
                'vue',
                'ts',
                'tsx',
                'md',
                'json',
                'yaml',
                'yml',
                'xml',
                'html',
                'css',
                'sql',
                'sh',
                'bash',
            ]
            const ext = file.name.split('.').pop()?.toLowerCase()

            if (!allowedMimeTypes.includes(file.type) && !allowedExtensions.includes(ext)) {
                this.showError(
                    'File type not supported. Allowed: images, PDF, text, CSV, and source code files.'
                )
                return false
            }

            const isImageOrPdf =
                file.type.startsWith('image/') ||
                file.type === 'application/pdf' ||
                ext === 'pdf' ||
                ['jpg', 'jpeg', 'png', 'gif', 'webp'].includes(ext)
            const maxSize = isImageOrPdf ? 10 * 1024 * 1024 : 2 * 1024 * 1024
            const maxSizeLabel = isImageOrPdf ? '10MB' : '2MB'

            if (file.size > maxSize) {
                this.showError(
                    `File is too large. Maximum size for ${
                        isImageOrPdf ? 'images/PDFs' : 'text files'
                    } is ${maxSizeLabel}.`
                )
                return false
            }

            return true
        },

        clearAttachedFile() {
            this.attachedFile = null
        },

        formatFileSize(bytes) {
            if (bytes === 0) return '0 B'
            const k = 1024
            const sizes = ['B', 'KB', 'MB', 'GB']
            const i = Math.floor(Math.log(bytes) / Math.log(k))
            return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
        },

        // Helper to get file icon based on mime type
        getFileIcon(mimeType) {
            if (mimeType?.startsWith('image/')) return 'fas fa-image'
            if (mimeType === 'application/pdf') return 'fas fa-file-pdf'
            if (mimeType === 'text/csv') return 'fas fa-file-csv'
            if (mimeType?.includes('python')) return 'fab fa-python'
            if (mimeType?.includes('php')) return 'fab fa-php'
            if (mimeType?.includes('javascript') || mimeType?.includes('typescript'))
                return 'fab fa-js'
            if (mimeType?.includes('json')) return 'fas fa-brackets-curly'
            if (mimeType?.includes('html')) return 'fab fa-html5'
            if (mimeType?.includes('css')) return 'fab fa-css3'
            if (mimeType?.includes('markdown')) return 'fas fa-file-alt'
            if (mimeType?.startsWith('text/')) return 'fas fa-file-code'
            return 'fas fa-file'
        },

        // Helper to get file icon class with color for templates
        // Accepts either a mimeType or a filename
        getFileIconClass(input) {
            if (!input) return 'fas fa-file text-tg-hint'

            // If input looks like a filename (has extension), extract extension
            let ext = ''
            if (input.includes('.') && !input.includes('/')) {
                ext = input.split('.').pop().toLowerCase()
            }

            // Check by extension first
            if (ext === 'pdf' || input === 'application/pdf')
                return 'fas fa-file-pdf text-red-500'
            if (ext === 'csv' || input === 'text/csv') return 'fas fa-file-csv text-green-500'
            if (ext === 'py' || input?.includes('python'))
                return 'fab fa-python text-yellow-500'
            if (ext === 'php' || input?.includes('php')) return 'fab fa-php text-purple-500'
            if (['js', 'jsx'].includes(ext) || input?.includes('javascript'))
                return 'fab fa-js text-yellow-400'
            if (['ts', 'tsx'].includes(ext) || input?.includes('typescript'))
                return 'fas fa-code text-blue-500'
            if (ext === 'vue' || input?.includes('vue')) return 'fab fa-vuejs text-green-500'
            if (ext === 'go' || input?.includes('go')) return 'fas fa-code text-cyan-500'
            if (ext === 'json' || input?.includes('json'))
                return 'fas fa-file-code text-yellow-600'
            if (ext === 'html' || input?.includes('html'))
                return 'fab fa-html5 text-orange-500'
            if (ext === 'css' || input?.includes('css')) return 'fab fa-css3 text-blue-400'
            if (ext === 'sql' || input?.includes('sql')) return 'fas fa-database text-blue-600'
            if (ext === 'sh' || input?.includes('shell'))
                return 'fas fa-terminal text-gray-400'
            if (['md', 'markdown'].includes(ext) || input?.includes('markdown'))
                return 'fas fa-file-alt text-tg-hint'
            if (['xml', 'yaml', 'yml'].includes(ext)) return 'fas fa-file-code text-orange-400'
            if (input?.startsWith('text/')) return 'fas fa-file-code text-tg-hint'
            return 'fas fa-file text-tg-hint'
        },

        // Handle window resize to adapt sidebar behavior
        handleWindowResize() {
            if (window.innerWidth >= 1024) {
                // Desktop: ensure sidebar is open
                this.sidebarOpen = true
            } else {
                // Mobile/tablet: close sidebar to prevent overlay issues
                // Note: We don't force close here to allow users to keep it open if they want
            }
        },

        // Copy message content to clipboard
        async copyMessage(content) {
            if (!content) return

            try {
                // Try modern clipboard API first
                if (navigator.clipboard && window.isSecureContext) {
                    await navigator.clipboard.writeText(content)
                } else {
                    // Fallback for older browsers or insecure contexts
                    const textArea = document.createElement('textarea')
                    textArea.value = content
                    textArea.style.position = 'fixed'
                    textArea.style.left = '-999999px'
                    textArea.style.top = '-999999px'
                    document.body.appendChild(textArea)
                    textArea.focus()
                    textArea.select()
                    document.execCommand('copy')
                    textArea.remove()
                }
            } catch (error) {
                console.error('Failed to copy message:', error)
                this.showError('Failed to copy message to clipboard')
            }
        },

        // Delete a message
        async deleteMessage(messageId) {
            if (!messageId) return

            // Confirm deletion
            if (!confirm('Are you sure you want to delete this message?')) {
                return
            }

            try {
                await this.apiCall(`/api/messages/${messageId}`, {
                    method: 'DELETE',
                })

                // Remove message from local array
                this.messages = this.messages.filter(m => m.id !== messageId)
            } catch (error) {
                console.error('Failed to delete message:', error)
                this.showError('Failed to delete message')
            }
        },

        // Format response time for display
        formatResponseTime(ms) {
            if (ms < 1000) {
                return `${ms}ms`
            } else {
                return `${(ms / 1000).toFixed(1)}s`
            }
        },

        // Format thread token totals for display
        formatThreadTokens(thread) {
            if (!thread.total_input_tokens && !thread.total_output_tokens) {
                return '0'
            }
            const total = (thread.total_input_tokens || 0) + (thread.total_output_tokens || 0)
            if (total < 1000) {
                return total.toString()
            } else if (total < 1000000) {
                return `${(total / 1000).toFixed(1)}k`
            } else {
                return `${(total / 1000000).toFixed(1)}M`
            }
        },

        // Open edit thread title modal
        editThreadTitle(threadId, currentTitle) {
            this.editingTitle = {
                id: threadId,
                title: currentTitle,
            }
            this.showEditTitle = true

            // Focus the input after modal opens
            this.$nextTick(() => {
                if (this.$refs.editTitleInput) {
                    this.$refs.editTitleInput.focus()
                    this.$refs.editTitleInput.select()
                }
            })
        },

        // Save thread title
        async saveThreadTitle() {
            if (!this.editingTitle.title.trim() || this.savingTitle) return

            this.savingTitle = true

            try {
                await this.apiCall(`/api/threads/${this.editingTitle.id}/title`, {
                    method: 'PUT',
                    body: JSON.stringify({ title: this.editingTitle.title.trim() }),
                })

                // Update the title in local threads array
                const activeThread = this.threads.find(t => t.id === this.editingTitle.id)
                if (activeThread) {
                    activeThread.title = this.editingTitle.title.trim()
                }

                // Update the title in archived threads array
                const archivedThread = this.archivedThreads.find(
                    t => t.id === this.editingTitle.id
                )
                if (archivedThread) {
                    archivedThread.title = this.editingTitle.title.trim()
                }

                // Update current thread if it's the one being edited
                if (this.currentThread && this.currentThread.id === this.editingTitle.id) {
                    this.currentThread.title = this.editingTitle.title.trim()
                }

                // Close the modal
                this.showEditTitle = false
            } catch (error) {
                this.showError('Failed to update thread title')
            } finally {
                this.savingTitle = false
            }
        },

        // Annotation display methods
        viewImageFullscreen(message) {
            if (!message.annotation_url || message.annotation_file_type !== 'image') {
                return
            }

            this.fullscreenImage = {
                src: message.annotation_url,
                filename: message.annotation_filename || 'annotation.png',
            }
        },

        closeFullscreenImage() {
            this.fullscreenImage = null
        },

        // Enhanced global keyboard shortcuts to include ESC for fullscreen
        handleGlobalKeyDown(event) {
            // Cmd+N (Mac) or Ctrl+N (Windows/Linux) to create new thread
            if ((event.metaKey || event.ctrlKey) && event.key === 'n') {
                event.preventDefault()
                this.newThread()
            }

            // ESC to close fullscreen image
            if (event.key === 'Escape' && this.fullscreenImage) {
                event.preventDefault()
                this.closeFullscreenImage()
            }

            // ESC to stop streaming
            if (event.key === 'Escape' && this.streaming) {
                event.preventDefault()
                this.stopStreaming()
            }
        },
    },
}).mount('#app')
