using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Media.Animation;
using System.Windows.Shapes;

namespace Nyxveil.App.Controls;

public class ConnectStatusRing : Control
{
    public static readonly DependencyProperty VisualStateProperty =
        DependencyProperty.Register(nameof(VisualState), typeof(string), typeof(ConnectStatusRing),
            new PropertyMetadata("Disconnected", OnVisualChanged));

    public static readonly DependencyProperty StatusTextProperty =
        DependencyProperty.Register(nameof(StatusText), typeof(string), typeof(ConnectStatusRing),
            new PropertyMetadata("Отключено"));

    public static readonly DependencyProperty TimerTextProperty =
        DependencyProperty.Register(nameof(TimerText), typeof(string), typeof(ConnectStatusRing),
            new PropertyMetadata("00:00:00"));

    public string VisualState
    {
        get => (string)GetValue(VisualStateProperty);
        set => SetValue(VisualStateProperty, value);
    }

    public string StatusText
    {
        get => (string)GetValue(StatusTextProperty);
        set => SetValue(StatusTextProperty, value);
    }

    public string TimerText
    {
        get => (string)GetValue(TimerTextProperty);
        set => SetValue(TimerTextProperty, value);
    }

    public override void OnApplyTemplate()
    {
        base.OnApplyTemplate();
        ApplyVisual(VisualState);
    }

    private static void OnVisualChanged(DependencyObject d, DependencyPropertyChangedEventArgs e)
    {
        if (d is ConnectStatusRing ring)
            ring.ApplyVisual(e.NewValue as string ?? "Disconnected");
    }

    private void ApplyVisual(string state)
    {
        var glow = GetTemplateChild("PART_Glow") as Ellipse;
        var ring = GetTemplateChild("PART_Ring") as Ellipse;
        var iconHost = GetTemplateChild("PART_Icon") as ContentControl;
        if (glow is null || ring is null) return;

        glow.BeginAnimation(OpacityProperty, null);
        ring.BeginAnimation(OpacityProperty, null);

        var teal = (Color)ColorConverter.ConvertFromString("#2DEED0")!;
        var err = (Color)ColorConverter.ConvertFromString("#E57373")!;

        switch (state)
        {
            case "Connected":
                ring.Stroke = new SolidColorBrush(teal);
                glow.Fill = new SolidColorBrush(Color.FromArgb(0x70, teal.R, teal.G, teal.B));
                glow.Opacity = 1;
                if (iconHost is not null) iconHost.Content = BuildShield(true);
                break;
            case "Connecting":
                ring.Stroke = new SolidColorBrush(teal);
                glow.Fill = new SolidColorBrush(Color.FromArgb(0x55, teal.R, teal.G, teal.B));
                var pulse = new DoubleAnimation(0.4, 1.0, new Duration(TimeSpan.FromMilliseconds(850)))
                {
                    AutoReverse = true,
                    RepeatBehavior = RepeatBehavior.Forever
                };
                glow.BeginAnimation(OpacityProperty, pulse);
                if (iconHost is not null) iconHost.Content = BuildShield(false);
                break;
            case "Error":
                ring.Stroke = new SolidColorBrush(err);
                glow.Fill = new SolidColorBrush(Color.FromArgb(0x50, err.R, err.G, err.B));
                glow.Opacity = 1;
                if (iconHost is not null) iconHost.Content = BuildShield(false);
                break;
            default:
                // Disconnected: same ring language, muted teal (not gray console).
                ring.Stroke = new SolidColorBrush(Color.FromArgb(0xAA, teal.R, teal.G, teal.B));
                glow.Fill = new SolidColorBrush(Color.FromArgb(0x38, teal.R, teal.G, teal.B));
                glow.Opacity = 1;
                if (iconHost is not null) iconHost.Content = BuildPower();
                break;
        }
    }

    private static FrameworkElement BuildShield(bool ok)
    {
        var grid = new Grid { Width = 36, Height = 36 };
        var path = new System.Windows.Shapes.Path
        {
            Fill = new SolidColorBrush((Color)ColorConverter.ConvertFromString("#2DEED0")!),
            Data = Geometry.Parse("M12,2 L20,5 V11.5 C20,16.5 16.5,21 12,22.5 C7.5,21 4,16.5 4,11.5 V5 Z"),
            Stretch = Stretch.Uniform,
            Width = 28,
            Height = 32,
            HorizontalAlignment = HorizontalAlignment.Center,
            VerticalAlignment = VerticalAlignment.Center
        };
        grid.Children.Add(path);
        if (ok)
        {
            grid.Children.Add(new System.Windows.Shapes.Path
            {
                Stroke = Brushes.White,
                StrokeThickness = 2.2,
                StrokeStartLineCap = PenLineCap.Round,
                StrokeEndLineCap = PenLineCap.Round,
                Data = Geometry.Parse("M8,12 L11,15 L17,9"),
                Stretch = Stretch.Uniform,
                Width = 16,
                Height = 12,
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center,
                Margin = new Thickness(0, 2, 0, 0)
            });
        }
        return grid;
    }

    private static FrameworkElement BuildPower()
    {
        return new System.Windows.Shapes.Path
        {
            Stroke = new SolidColorBrush((Color)ColorConverter.ConvertFromString("#2DEED0")!),
            StrokeThickness = 2.4,
            StrokeStartLineCap = PenLineCap.Round,
            StrokeEndLineCap = PenLineCap.Round,
            Data = Geometry.Parse("M12,3 V11 M7.5,6.2 A6.5,6.5 0 1 0 16.5,6.2"),
            Stretch = Stretch.Uniform,
            Width = 28,
            Height = 28
        };
    }
}
