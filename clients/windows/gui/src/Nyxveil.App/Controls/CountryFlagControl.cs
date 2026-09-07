using System.Windows;
using System.Windows.Controls;
using Nyxveil.App.Services;

namespace Nyxveil.App.Controls;

public class CountryFlagControl : ContentControl
{
    public static readonly DependencyProperty CountryCodeProperty =
        DependencyProperty.Register(nameof(CountryCode), typeof(string), typeof(CountryFlagControl),
            new PropertyMetadata("", OnCodeChanged));

    public static readonly DependencyProperty FlagSizeProperty =
        DependencyProperty.Register(nameof(FlagSize), typeof(double), typeof(CountryFlagControl),
            new PropertyMetadata(36.0, OnCodeChanged));

    public string CountryCode
    {
        get => (string)GetValue(CountryCodeProperty);
        set => SetValue(CountryCodeProperty, value);
    }

    public double FlagSize
    {
        get => (double)GetValue(FlagSizeProperty);
        set => SetValue(FlagSizeProperty, value);
    }

    private static void OnCodeChanged(DependencyObject d, DependencyPropertyChangedEventArgs e)
    {
        if (d is CountryFlagControl c)
            c.Content = FlagService.CreateFlag(c.CountryCode, c.FlagSize);
    }

    public CountryFlagControl()
    {
        Content = FlagService.CreateFlag("", 36);
    }
}
