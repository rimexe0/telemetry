package cyou.rime.telemetry.collector;

import android.app.Activity;
import android.os.Bundle;
import android.widget.TextView;

/** Explains the declared Health Connect permissions on Android 13 and earlier. */
public final class PermissionRationaleActivity extends Activity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        TextView text = new TextView(this);
        int padding = (int) (24 * getResources().getDisplayMetrics().density);
        text.setPadding(padding, padding, padding, padding);
        text.setText("Telemetry requests only the Health Connect records enabled by the owner. Owner-triggered reads stay on this device in a durable local queue until device provisioning enables tailnet delivery to the owner's Telemetry server. This app does not diagnose, treat, or share health data with other parties.");
        text.setTextSize(18);
        setContentView(text);
    }
}
