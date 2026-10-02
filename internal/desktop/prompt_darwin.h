// Declarations shared by prompt_darwin.m and prompt_darwin.go.
void td_prompt_show(const char *title, const char *detail, const char **options, int count, double seconds);
void td_prompt_close(void);
extern void tdPromptChoice(int index);
